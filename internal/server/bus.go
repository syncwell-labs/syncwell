package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"syncwell/internal/crdt"
)

// ClusterEvent is a room-level event that can be fanned out across processes.
// The current transport is intentionally tiny: it carries the room id, event
// kind, origin node, and enough payload to reapply the change on another node.
type ClusterEvent struct {
	Room     string                   `json:"room"`
	Kind     string                   `json:"kind"` // "op" | "presence" | "snapshot"
	From     string                   `json:"from"`
	Epoch    uint64                   `json:"epoch,omitempty"`
	Key      string                   `json:"key,omitempty"`
	ClientID string                   `json:"clientId,omitempty"`
	Register *crdt.Register           `json:"register,omitempty"`
	Data     json.RawMessage          `json:"data,omitempty"`
	State    map[string]crdt.Register `json:"state,omitempty"`
}

// ClusterBus is a pub/sub backbone used to fan room events across processes.
type ClusterBus interface {
	Subscribe(room string) (<-chan ClusterEvent, func())
	Publish(ev ClusterEvent)
}

// ClusterRelay sends cluster events to peer processes over HTTP.
type ClusterRelay interface {
	Publish(ev ClusterEvent)
}

// MemoryClusterBus is an in-process bus for local sharding experiments and
// tests. It is deliberately simple, but it models the shape of a real pub/sub
// backbone closely enough for the sharding foundation.
type MemoryClusterBus struct {
	mu   sync.RWMutex
	subs map[string]map[chan ClusterEvent]struct{}
}

// NewMemoryClusterBus returns an empty in-process pub/sub bus.
func NewMemoryClusterBus() *MemoryClusterBus {
	return &MemoryClusterBus{subs: map[string]map[chan ClusterEvent]struct{}{}}
}

func (b *MemoryClusterBus) Subscribe(room string) (<-chan ClusterEvent, func()) {
	ch := make(chan ClusterEvent, 32)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[string]map[chan ClusterEvent]struct{}{}
	}
	if _, ok := b.subs[room]; !ok {
		b.subs[room] = map[chan ClusterEvent]struct{}{}
	}
	b.subs[room][ch] = struct{}{}
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if roomSubs, ok := b.subs[room]; ok {
			if _, ok := roomSubs[ch]; ok {
				delete(roomSubs, ch)
			}
			if len(roomSubs) == 0 {
				delete(b.subs, room)
			}
		}
	}
}

func (b *MemoryClusterBus) Publish(ev ClusterEvent) {
	b.mu.RLock()
	subs := b.subs[ev.Room]
	out := make([]chan ClusterEvent, 0, len(subs))
	for ch := range subs {
		out = append(out, ch)
	}
	b.mu.RUnlock()
	for _, ch := range out {
		select {
		case ch <- ev:
		default:
		}
	}
}

// MarshalClusterEvent serializes an event into the wire format used by the
// eventual network transport.
func MarshalClusterEvent(ev ClusterEvent) ([]byte, error) {
	return json.Marshal(ev)
}

// UnmarshalClusterEvent parses the wire format back into an event.
func UnmarshalClusterEvent(b []byte) (ClusterEvent, error) {
	var ev ClusterEvent
	err := json.Unmarshal(b, &ev)
	return ev, err
}

// HTTPClusterRelay forwards cluster events to peer HTTP endpoints. It is a
// minimal, fail-soft transport used to bridge the local pub/sub bus with an
// eventual multi-process deployment.
type HTTPClusterRelay struct {
	Peers  []string
	Path   string
	Client *http.Client
	Token  string // optional bearer token sent to peers (cluster admin token)
	sem    chan struct{}

	posted  atomic.Uint64
	dropped atomic.Uint64
	errs    atomic.Uint64

	mu           sync.RWMutex
	peerUp       map[string]bool
	peerDuration map[string]*histogram
	peerErrors   map[string]map[string]uint64
}

// NewHTTPClusterRelay returns a relay with sensible defaults. Publishing is
// asynchronous and bounded so a slow or hung peer can never stall the single
// room goroutine that calls Publish.
func NewHTTPClusterRelay(peers []string, path string, client *http.Client) *HTTPClusterRelay {
	if path == "" {
		path = "/cluster/event"
	}
	client = newClusterHTTPClient(client)
	out := make([]string, 0, len(peers))
	seen := map[string]struct{}{}
	for _, peer := range peers {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}
		if _, ok := seen[peer]; ok {
			continue
		}
		seen[peer] = struct{}{}
		out = append(out, strings.TrimRight(peer, "/"))
	}
	up := make(map[string]bool, len(out))
	for _, peer := range out {
		up[peer] = false // unknown until the first delivery attempt
	}
	return &HTTPClusterRelay{Peers: out, Path: path, Client: client, sem: make(chan struct{}, 64), peerUp: up, peerDuration: map[string]*histogram{}, peerErrors: map[string]map[string]uint64{}}
}

func newClusterHTTPClient(base *http.Client) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if base != nil {
		if supplied, ok := base.Transport.(*http.Transport); ok {
			transport = supplied.Clone()
		} else if base.Transport != nil {
			transport = http.DefaultTransport.(*http.Transport).Clone()
		}
	}
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	return &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Publish enqueues the event for asynchronous delivery. If the outbound queue is
// saturated the event is dropped (and logged) rather than blocking the room.
func (r *HTTPClusterRelay) Publish(ev ClusterEvent) {
	if r == nil || len(r.Peers) == 0 {
		return
	}
	select {
	case r.sem <- struct{}{}:
		go func() {
			defer func() { <-r.sem }()
			r.post(ev)
		}()
	default:
		r.dropped.Add(1)
		LogEvent("cluster_relay_dropped", map[string]any{"room": ev.Room, "kind": ev.Kind})
	}
}

// Metrics returns relay counters for /metrics. inflight approximates cluster
// lag: events accepted but not yet POSTed to peers.
func (r *HTTPClusterRelay) Metrics() (posted, dropped, errs, inflight uint64) {
	return r.posted.Load(), r.dropped.Load(), r.errs.Load(), uint64(len(r.sem))
}

// PeerUp reports whether the most recent post to each peer succeeded.
func (r *HTTPClusterRelay) PeerUp() map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]bool, len(r.peerUp))
	for peer, up := range r.peerUp {
		out[peer] = up
	}
	return out
}

func (r *HTTPClusterRelay) setPeerUp(peer string, up bool) {
	r.mu.Lock()
	if r.peerUp == nil {
		r.peerUp = map[string]bool{}
	}
	r.peerUp[peer] = up
	r.mu.Unlock()
}

func (r *HTTPClusterRelay) recordPeer(peer, reason string, elapsed time.Duration, up bool) {
	r.mu.Lock()
	h := r.peerDuration[peer]
	if h == nil {
		h = newHistogram([]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2})
		r.peerDuration[peer] = h
	}
	if reason != "" {
		if r.peerErrors[peer] == nil {
			r.peerErrors[peer] = map[string]uint64{}
		}
		r.peerErrors[peer][reason]++
	}
	r.peerUp[peer] = up
	r.mu.Unlock()
	h.observe(elapsed.Seconds())
}

// WritePeerMetrics exports metrics with labels bounded by the configured peer list.
func (r *HTTPClusterRelay) WritePeerMetrics(w io.Writer) {
	r.mu.RLock()
	durations := make(map[string]*histogram, len(r.peerDuration))
	for peer, h := range r.peerDuration {
		durations[peer] = h
	}
	errs := make(map[string]map[string]uint64, len(r.peerErrors))
	for peer, reasons := range r.peerErrors {
		copyReasons := map[string]uint64{}
		for reason, n := range reasons {
			copyReasons[reason] = n
		}
		errs[peer] = copyReasons
	}
	r.mu.RUnlock()
	fmt.Fprintln(w, "# HELP syncwell_cluster_relay_duration_seconds Time spent delivering one event to a configured peer")
	fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_duration_seconds histogram")
	for peer, h := range durations {
		h.writeSamplesWithLabel(w, "syncwell_cluster_relay_duration_seconds", "peer", peer)
	}
	fmt.Fprintln(w, "# HELP syncwell_cluster_relay_errors_total Failed relay attempts by configured peer and bounded reason")
	fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_errors_total counter")
	for peer, reasons := range errs {
		for reason, n := range reasons {
			fmt.Fprintf(w, "syncwell_cluster_relay_errors_total{peer=%q,reason=%q} %d\n", peer, reason, n)
		}
	}
}

func (r *HTTPClusterRelay) post(ev ClusterEvent) {
	body, err := MarshalClusterEvent(ev)
	if err != nil {
		LogEvent("cluster_wire_marshal_error", map[string]any{"room": ev.Room, "error": err.Error()})
		return
	}
	for _, peer := range r.Peers {
		r.postToPeer(peer, ev, body)
	}
}

func (r *HTTPClusterRelay) postToPeer(peer string, ev ClusterEvent, body []byte) {
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	started := time.Now()
	ctx, span := otel.Tracer("syncwell").Start(context.Background(), "cluster.post",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("syncwell.peer", peer),
			attribute.String("syncwell.room", ev.Room),
			attribute.String("syncwell.kind", ev.Kind),
		),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peer+r.Path, bytes.NewReader(body))
	if err != nil {
		span.SetStatus(codes.Error, "request build failed")
		span.End()
		r.errs.Add(1)
		r.recordPeer(peer, "request", time.Since(started), false)
		LogEvent("cluster_relay_request_error", map[string]any{"peer": peer, "room": ev.Room, "error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, "post failed")
		span.RecordError(err)
		span.End()
		r.errs.Add(1)
		r.recordPeer(peer, "transport", time.Since(started), false)
		LogEvent("cluster_relay_post_error", map[string]any{"peer": peer, "room": ev.Room, "error": err.Error()})
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		span.SetStatus(codes.Error, "peer returned "+resp.Status)
		span.End()
		r.errs.Add(1)
		r.recordPeer(peer, "status", time.Since(started), false)
		LogEvent("cluster_relay_status_error", map[string]any{"peer": peer, "room": ev.Room, "status": resp.StatusCode})
		return
	}
	span.End()
	r.posted.Add(1)
	r.recordPeer(peer, "", time.Since(started), true)
}

// AntiEntropy exchanges bounded pages of per-register digests and sends only
// differing registers. A full snapshot is the bounded fallback.
func (r *HTTPClusterRelay) AntiEntropy(ev ClusterEvent, digest string, apply func(ClusterEvent)) uint64 {
	snapshotBody, err := MarshalClusterEvent(ev)
	if err != nil {
		return 0
	}
	var transferred uint64
	for _, peer := range r.Peers {
		cursor := ""
		fallback := false
		for pageNo := 0; pageNo < 4096; pageNo++ {
			localPage := digestPage(ev.State, cursor)
			requestBody, err := json.Marshal(clusterDigestRequest{Room: ev.Room, From: ev.From, Digest: digest, Cursor: cursor, Entries: digestEntries(localPage)})
			if err != nil || len(requestBody) > 1<<20 {
				fallback = true
				break
			}
			started := time.Now()
			req, err := http.NewRequest(http.MethodPost, peer+"/cluster/digest", bytes.NewReader(requestBody))
			if err != nil {
				r.errs.Add(1)
				r.recordPeer(peer, "request", time.Since(started), false)
				fallback = true
				break
			}
			req.Header.Set("Content-Type", "application/json")
			if r.Token != "" {
				req.Header.Set("Authorization", "Bearer "+r.Token)
			}
			client := r.Client
			if client == nil {
				client = &http.Client{Timeout: 2 * time.Second}
			}
			resp, err := client.Do(req)
			if err != nil {
				r.errs.Add(1)
				r.recordPeer(peer, "digest_transport", time.Since(started), false)
				break
			}
			responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
			_ = resp.Body.Close()
			transferred += uint64(len(requestBody) + len(responseBody))
			if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
				r.errs.Add(1)
				r.recordPeer(peer, "digest_status", time.Since(started), false)
				break
			}
			var reply clusterDigestResponse
			if err := json.Unmarshal(responseBody, &reply); err != nil || len(responseBody) > 1<<20 {
				r.errs.Add(1)
				r.recordPeer(peer, "digest_response", time.Since(started), false)
				fallback = true
				break
			}
			r.recordPeer(peer, "", time.Since(started), true)
			if reply.TooLarge {
				fallback = true
				break
			}
			remoteDelta := ClusterEvent{Room: ev.Room, Kind: "delta", From: peer, State: reply.Missing}
			if len(remoteDelta.State) > 0 && apply != nil {
				apply(remoteDelta)
			}
			localDelta := make(map[string]crdt.Register)
			for key, reg := range localPage {
				if reply.Entries[key] != digestEntry(reg) {
					localDelta[key] = reg
				}
			}
			if len(localDelta) > 0 {
				delta := ClusterEvent{Room: ev.Room, Kind: "delta", From: ev.From, State: localDelta}
				deltaBody, marshalErr := MarshalClusterEvent(delta)
				if marshalErr != nil || len(deltaBody) >= len(snapshotBody) || len(deltaBody) > 1<<20 {
					fallback = true
					break
				}
				r.postToPeer(peer, delta, deltaBody)
				transferred += uint64(len(deltaBody))
			}
			localLast, remoteLast := lastDigestKey(localPage), lastDigestKey(registersFromDigests(reply.Entries, localPage, reply.Missing))
			if localLast == "" && remoteLast == "" {
				break
			}
			next := localLast
			if next == "" || (remoteLast != "" && remoteLast < next) {
				next = remoteLast
			}
			if next <= cursor {
				fallback = true
				break
			}
			cursor = next
			if pageNo == 4095 {
				fallback = true
			}
		}
		if fallback {
			transferred += r.postSnapshotFallback(peer, ev)
		}
	}
	return transferred
}

// postSnapshotFallback keeps each fallback event within the cluster ingest
// body limit, including rooms whose full snapshot exceeds that limit.
func (r *HTTPClusterRelay) postSnapshotFallback(peer string, ev ClusterEvent) uint64 {
	keys := make([]string, 0, len(ev.State))
	for key := range ev.State {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	chunk := make(map[string]crdt.Register)
	var transferred uint64
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		part := ClusterEvent{Room: ev.Room, Kind: "snapshot", From: ev.From, Epoch: ev.Epoch, State: chunk}
		body, err := MarshalClusterEvent(part)
		if err == nil && len(body) <= 1<<20 {
			r.postToPeer(peer, part, body)
			transferred += uint64(len(body))
		}
		chunk = make(map[string]crdt.Register)
	}
	for _, key := range keys {
		chunk[key] = ev.State[key]
		part := ClusterEvent{Room: ev.Room, Kind: "snapshot", From: ev.From, Epoch: ev.Epoch, State: chunk}
		body, err := MarshalClusterEvent(part)
		if err == nil && len(body) <= 1<<20 {
			continue
		}
		delete(chunk, key)
		flush()
		chunk[key] = ev.State[key]
		part.State = chunk
		body, err = MarshalClusterEvent(part)
		if err != nil || len(body) > 1<<20 {
			delete(chunk, key)
			LogEvent("cluster_snapshot_register_too_large", map[string]any{"room": ev.Room})
		}
	}
	flush()
	return transferred
}

func registersFromDigests(entries map[string]string, local map[string]crdt.Register, missing map[string]crdt.Register) map[string]crdt.Register {
	page := make(map[string]crdt.Register, len(entries)+len(missing))
	for key := range entries {
		if reg, ok := local[key]; ok {
			page[key] = reg
		}
	}
	for key, reg := range missing {
		page[key] = reg
	}
	return page
}
