package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"syncwell/internal/crdt"
	"syncwell/internal/store"
)

func TestDigestAntiEntropyFallsBackOnceThenSendsOnlyDigest(t *testing.T) {
	state := map[string]crdt.Register{"title": serverReg("hello", 1, "node-a")}
	digest := DigestRegisters(state)
	if digest != DigestRegisters(map[string]crdt.Register{"title": state["title"]}) {
		t.Fatal("digest must not depend on map iteration or identity")
	}
	other := map[string]crdt.Register{"title": serverReg("different", 1, "node-a")}
	if DigestRegisters(other) == digest {
		t.Fatal("value changes must change the digest")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := NewHub(Config{Store: st, AdminToken: "cluster-secret", IdleRoomTTL: -1})
	mux := http.NewServeMux()
	mux.Handle("/cluster/event", h.ClusterHandler(h.ServeClusterEvent))
	mux.Handle("/cluster/digest", h.ClusterHandler(h.ServeClusterDigest))
	peer := httptest.NewServer(mux)
	defer peer.Close()
	relay := NewHTTPClusterRelay([]string{peer.URL}, "", peer.Client())
	relay.Token = "cluster-secret"
	ev := ClusterEvent{Room: "digest-room", Kind: "snapshot", From: "node-a", State: state}
	firstBytes := relay.AntiEntropy(ev, digest, nil)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		loaded, e := st.Load("digest-room")
		if e == nil && loaded["title"].Equal(state["title"]) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	loaded, err := st.Load("digest-room")
	if err != nil || !loaded["title"].Equal(state["title"]) {
		t.Fatalf("peer did not converge: %v err=%v", loaded, err)
	}
	secondBytes := relay.AntiEntropy(ev, digest, nil)
	if firstBytes <= secondBytes {
		t.Fatalf("digest exchange did not reduce bytes after convergence: first=%d second=%d", firstBytes, secondBytes)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDigestAntiEntropyPaginatesAndSendsOnlyChangedRegister(t *testing.T) {
	state := make(map[string]crdt.Register, 300)
	for i := 0; i < 300; i++ {
		state[fmt.Sprintf("key-%03d", i)] = serverReg(strings.Repeat("v", 256), uint64(i+1), "node-a")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := NewHub(Config{Store: st, AdminToken: "secret", IdleRoomTTL: -1})
	mux := http.NewServeMux()
	mux.Handle("/cluster/digest", h.ClusterHandler(h.ServeClusterDigest))
	mux.Handle("/cluster/event", h.ClusterHandler(h.ServeClusterEvent))
	peer := httptest.NewServer(mux)
	defer peer.Close()
	relay := NewHTTPClusterRelay([]string{peer.URL}, "", peer.Client())
	relay.Token = "secret"
	ev := ClusterEvent{Room: "delta-room", Kind: "snapshot", From: "node-a", State: state}
	if n := relay.AntiEntropy(ev, DigestRegisters(state), nil); n == 0 {
		t.Fatal("initial sync transferred no bytes")
	}
	remoteOnly := serverReg("peer-only", 900, "node-b")
	if err := h.ingestClusterEvent(ClusterEvent{Room: ev.Room, Kind: "delta", From: "node-b", State: map[string]crdt.Register{"peer-only": remoteOnly}}); err != nil {
		t.Fatal(err)
	}
	state["key-037"] = serverReg("changed", 100, "node-a")
	ev.State = state
	localStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	localHub := NewHub(Config{Store: localStore, IdleRoomTTL: -1})
	got := relay.AntiEntropy(ev, DigestRegisters(state), func(delta ClusterEvent) { _ = localHub.ingestClusterEvent(delta) })
	full, _ := MarshalClusterEvent(ev)
	if got >= uint64(len(full)) {
		t.Fatalf("changed-register exchange was not smaller than full snapshot: got=%d full=%d", got, len(full))
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		loaded, e := st.Load("delta-room")
		if e == nil && loaded["key-037"].Equal(state["key-037"]) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	loaded, err := st.Load("delta-room")
	if err != nil || !loaded["key-037"].Equal(state["key-037"]) {
		t.Fatalf("delta did not converge: err=%v", err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		loaded, e := localStore.Load("delta-room")
		if e == nil && loaded["peer-only"].Equal(remoteOnly) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	loaded, err = localStore.Load("delta-room")
	if err != nil || !loaded["peer-only"].Equal(remoteOnly) {
		t.Fatalf("remote-only register was not applied locally: err=%v", err)
	}
	ctxLocal, cancelLocal := context.WithTimeout(context.Background(), time.Second)
	defer cancelLocal()
	if err := localHub.Shutdown(ctxLocal); err != nil {
		t.Fatal(err)
	}
	if err := localStore.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDigestAntiEntropyChunksOversizedSnapshotFallback(t *testing.T) {
	state := make(map[string]crdt.Register, 110)
	for i := 0; i < 110; i++ {
		state[fmt.Sprintf("large-%03d", i)] = serverReg(strings.Repeat("v", 10<<10), uint64(i+1), "node-a")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := NewHub(Config{Store: st, AdminToken: "secret", IdleRoomTTL: -1})
	mux := http.NewServeMux()
	mux.Handle("/cluster/digest", h.ClusterHandler(h.ServeClusterDigest))
	mux.Handle("/cluster/event", h.ClusterHandler(h.ServeClusterEvent))
	peer := httptest.NewServer(mux)
	defer peer.Close()
	relay := NewHTTPClusterRelay([]string{peer.URL}, "", peer.Client())
	relay.Token = "secret"
	ev := ClusterEvent{Room: "large-delta-room", Kind: "snapshot", From: "node-a", State: state}
	full, err := MarshalClusterEvent(ev)
	if err != nil || len(full) <= 1<<20 {
		t.Fatalf("fixture snapshot must exceed cluster body limit: size=%d err=%v", len(full), err)
	}
	relay.AntiEntropy(ev, DigestRegisters(state), nil)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		loaded, e := st.Load(ev.Room)
		if e == nil && len(loaded) == len(state) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	loaded, err := st.Load(ev.Room)
	if err != nil || len(loaded) != len(state) {
		t.Fatalf("chunked fallback did not converge: got=%d want=%d err=%v", len(loaded), len(state), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDigestAntiEntropySavesBytesUnderTenPercentEventLoss(t *testing.T) {
	state := make(map[string]crdt.Register, 64)
	for i := 0; i < 64; i++ {
		state[fmt.Sprintf("key-%03d", i)] = crdt.Register{Value: []byte(`"` + strings.Repeat("v", 1024) + `"`), TS: crdt.Timestamp{Counter: uint64(i + 1), Node: "node-a"}}
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := NewHub(Config{Store: st, AdminToken: "secret", IdleRoomTTL: -1})
	var dropped atomic.Bool
	mux := http.NewServeMux()
	mux.Handle("/cluster/digest", h.ClusterHandler(h.ServeClusterDigest))
	mux.Handle("/cluster/event", h.ClusterHandler(func(w http.ResponseWriter, r *http.Request) {
		if dropped.CompareAndSwap(false, true) {
			http.Error(w, "simulated loss", http.StatusServiceUnavailable)
			return
		}
		h.ServeClusterEvent(w, r)
	}))
	peer := httptest.NewServer(mux)
	defer peer.Close()
	relay := NewHTTPClusterRelay([]string{peer.URL}, "", peer.Client())
	relay.Token = "secret"
	ev := ClusterEvent{Room: "loss-room", Kind: "snapshot", From: "node-a", State: state}
	digest := DigestRegisters(state)
	var total uint64
	for i := 0; i < 10; i++ {
		total += relay.AntiEntropy(ev, digest, nil)
	}
	full, _ := MarshalClusterEvent(ev)
	baseline := uint64(len(full) * 10)
	if !dropped.Load() || total >= baseline {
		t.Fatalf("digest traffic did not beat full-snapshot baseline under 10%% packet loss: digest=%d baseline=%d", total, baseline)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		loaded, e := st.Load("loss-room")
		if e == nil && len(loaded) == len(state) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	loaded, err := st.Load("loss-room")
	if err != nil || len(loaded) != len(state) {
		t.Fatalf("did not converge after digest retry: registers=%d err=%v", len(loaded), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDigestAntiEntropyImprovesConvergenceTimeUnderLoss(t *testing.T) {
	base := make(map[string]crdt.Register, 32)
	for i := 0; i < 32; i++ {
		base[fmt.Sprintf("key-%03d", i)] = serverReg(strings.Repeat("v", 8<<10), uint64(i+1), "node-a")
	}
	updated := make(map[string]crdt.Register, len(base))
	for key, reg := range base {
		updated[key] = reg
	}
	states := make([]map[string]crdt.Register, 10)
	for tick := range states {
		state := make(map[string]crdt.Register, len(updated))
		for key, reg := range updated {
			state[key] = reg
		}
		key := fmt.Sprintf("key-%03d", tick)
		state[key] = serverReg(strings.Repeat("x", 8<<10), uint64(1000+tick), "node-a")
		states[tick] = state
		updated = state
	}

	newPeer := func() (*store.FileStore, *Hub, *httptest.Server, *atomic.Int32) {
		t.Helper()
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for key, reg := range base {
			if err := st.Append("timing-room", store.Record{Key: key, Reg: reg}); err != nil {
				t.Fatal(err)
			}
		}
		h := NewHub(Config{Store: st, AdminToken: "secret", IdleRoomTTL: -1})
		attempts := &atomic.Int32{}
		mux := http.NewServeMux()
		mux.Handle("/cluster/digest", h.ClusterHandler(h.ServeClusterDigest))
		mux.Handle("/cluster/event", h.ClusterHandler(func(w http.ResponseWriter, r *http.Request) {
			// Model transfer time at 100 MB/s so the payload-size effect is
			// visible without relying on external network conditions.
			time.Sleep(time.Duration(r.ContentLength) * 10 * time.Nanosecond)
			n := attempts.Add(1)
			if n == 1 {
				http.Error(w, "simulated 10% loss", http.StatusServiceUnavailable)
				return
			}
			h.ServeClusterEvent(w, r)
		}))
		srv := httptest.NewServer(mux)
		return st, h, srv, attempts
	}
	converged := func(st *store.FileStore, expected map[string]crdt.Register) bool {
		loaded, err := st.Load("timing-room")
		if err != nil || len(loaded) != len(expected) {
			return false
		}
		for key, reg := range expected {
			if !loaded[key].Equal(reg) {
				return false
			}
		}
		return true
	}
	waitConverged := func(st *store.FileStore, expected map[string]crdt.Register) bool {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if converged(st, expected) {
				return true
			}
			time.Sleep(time.Millisecond)
		}
		return converged(st, expected)
	}

	baseStore, baseHub, baseServer, _ := newPeer()
	baseStart := time.Now()
	var baselineBytes uint64
	var baselineTime time.Duration
	for tick, state := range states {
		ev := ClusterEvent{Room: "timing-room", Kind: "snapshot", From: "node-a", State: state}
		fullBody, err := MarshalClusterEvent(ev)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, baseServer.URL+"/cluster/event", bytes.NewReader(fullBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := baseServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		baselineBytes += uint64(len(fullBody))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if tick != 0 {
				t.Fatalf("full snapshot attempt %d failed: %s", tick+1, resp.Status)
			}
		}
	}
	if !waitConverged(baseStore, updated) {
		loaded, loadErr := baseStore.Load("timing-room")
		for key, want := range updated {
			if !loaded[key].Equal(want) {
				t.Logf("baseline mismatch key=%s got=%+v want=%+v", key, loaded[key].TS, want.TS)
			}
		}
		t.Fatalf("full-snapshot baseline did not converge: keys=%d err=%v", len(loaded), loadErr)
	}
	baselineTime = time.Since(baseStart)
	baseServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := baseHub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := baseStore.Close(); err != nil {
		t.Fatal(err)
	}

	deltaStore, deltaHub, deltaServer, _ := newPeer()
	deltaRelay := NewHTTPClusterRelay([]string{deltaServer.URL}, "", deltaServer.Client())
	deltaRelay.Token = "secret"
	deltaStart := time.Now()
	var deltaBytes uint64
	var deltaTime time.Duration
	for _, state := range states {
		ev := ClusterEvent{Room: "timing-room", Kind: "snapshot", From: "node-a", State: state}
		deltaBytes += deltaRelay.AntiEntropy(ev, DigestRegisters(state), nil)
	}
	if !waitConverged(deltaStore, updated) {
		t.Fatal("digest delta did not converge")
	}
	deltaTime = time.Since(deltaStart)
	if deltaBytes >= baselineBytes {
		t.Fatalf("delta bytes=%d did not beat full snapshot baseline=%d", deltaBytes, baselineBytes)
	}
	if deltaTime >= baselineTime {
		t.Fatalf("delta convergence=%s did not beat full snapshot baseline=%s", deltaTime, baselineTime)
	}
	deltaServer.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	if err := deltaHub.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := deltaStore.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("10%% loss comparison: full snapshot %d bytes/%s; digest delta %d bytes/%s", baselineBytes, baselineTime, deltaBytes, deltaTime)
}

func TestServeClusterDigestRejectsMalformedDigest(t *testing.T) {
	h := NewHub(Config{AdminToken: "secret"})
	body, _ := json.Marshal(clusterDigestRequest{Room: "room", From: "peer", Digest: "not-a-digest"})
	req := httptest.NewRequest(http.MethodPost, "/cluster/digest", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	h.ServeClusterDigest(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
