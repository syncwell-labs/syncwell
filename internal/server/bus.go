package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

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
}

// NewHTTPClusterRelay returns a relay with sensible defaults. Publishing is
// asynchronous and bounded so a slow or hung peer can never stall the single
// room goroutine that calls Publish.
func NewHTTPClusterRelay(peers []string, path string, client *http.Client) *HTTPClusterRelay {
	if path == "" {
		path = "/cluster/event"
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
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
	return &HTTPClusterRelay{Peers: out, Path: path, Client: client, sem: make(chan struct{}, 64)}
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
		LogEvent("cluster_relay_dropped", map[string]any{"room": ev.Room, "kind": ev.Kind})
	}
}

func (r *HTTPClusterRelay) post(ev ClusterEvent) {
	body, err := MarshalClusterEvent(ev)
	if err != nil {
		LogEvent("cluster_wire_marshal_error", map[string]any{"room": ev.Room, "error": err.Error()})
		return
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	for _, peer := range r.Peers {
		req, err := http.NewRequest(http.MethodPost, peer+r.Path, bytes.NewReader(body))
		if err != nil {
			LogEvent("cluster_relay_request_error", map[string]any{"peer": peer, "room": ev.Room, "error": err.Error()})
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if r.Token != "" {
			req.Header.Set("Authorization", "Bearer "+r.Token)
		}
		resp, err := client.Do(req)
		if err != nil {
			LogEvent("cluster_relay_post_error", map[string]any{"peer": peer, "room": ev.Room, "error": err.Error()})
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			LogEvent("cluster_relay_status_error", map[string]any{"peer": peer, "room": ev.Room, "status": resp.StatusCode})
		}
	}
}
