package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"syncwell/internal/crdt"
	"syncwell/internal/store"
)

func TestMemoryClusterBusRelaysOpBetweenHubs(t *testing.T) {
	bus := NewMemoryClusterBus()
	h1 := NewHub(Config{Bus: bus, NodeID: "node-a"})
	h2 := NewHub(Config{Bus: bus, NodeID: "node-b"})

	r1, err := h1.room("demo")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := h2.room("demo")
	if err != nil {
		t.Fatal(err)
	}

	writer := &Client{
		id:   "writer",
		room: r1,
		send: make(chan []byte, 4),
		claims: TokenClaims{
			Caps: map[string]bool{CapRead: true, CapWrite: true},
		},
	}
	reader := &Client{
		id:   "reader",
		room: r2,
		send: make(chan []byte, 4),
		claims: TokenClaims{
			Caps: map[string]bool{CapRead: true},
		},
	}

	r1.cmds <- command{kind: "join", client: writer}
	<-writer.send
	r2.cmds <- command{kind: "join", client: reader}
	<-reader.send

	r1.cmds <- command{
		kind:   "op",
		client: writer,
		in: Inbound{
			Type:  "op",
			Epoch: r1.epoch,
			Key:   "title",
			Register: &crdt.Register{
				Value:   []byte(`"hello"`),
				TS:      crdt.Timestamp{Counter: 1, Node: "writer"},
				Deleted: false,
			},
		},
	}

	select {
	case msg := <-reader.send:
		if !strings.Contains(string(msg), `"type":"op"`) {
			t.Fatalf("expected relayed op frame, got %s", msg)
		}
		if !strings.Contains(string(msg), `"title"`) {
			t.Fatalf("expected relayed room key, got %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive relayed op from peer hub")
	}
}

func TestMemoryClusterBusIsolatesRooms(t *testing.T) {
	bus := NewMemoryClusterBus()
	chA, stopA := bus.Subscribe("room-a")
	defer stopA()
	chB, stopB := bus.Subscribe("room-b")
	defer stopB()

	bus.Publish(ClusterEvent{Room: "room-a", Kind: "presence", From: "node-a"})

	select {
	case <-chA:
	case <-time.After(time.Second):
		t.Fatal("room-a subscriber did not receive its event")
	}

	select {
	case <-chB:
		t.Fatal("room-b subscriber received an event for room-a")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestClusterEventWireRoundTrip(t *testing.T) {
	ev := ClusterEvent{
		Room:     "demo",
		Kind:     "op",
		From:     "node-a",
		Epoch:    7,
		Key:      "title",
		ClientID: "c1",
		Register: &crdt.Register{
			Value:   json.RawMessage(`"hello"`),
			TS:      crdt.Timestamp{Counter: 12, Node: "c1"},
			Deleted: false,
		},
		Data:  json.RawMessage(`{"cursor":1}`),
		State: map[string]crdt.Register{"title": {Value: json.RawMessage(`"hello"`), TS: crdt.Timestamp{Counter: 12, Node: "c1"}}},
	}

	wire, err := MarshalClusterEvent(ev)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	got, err := UnmarshalClusterEvent(wire)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if got.Room != ev.Room || got.Kind != ev.Kind || got.From != ev.From || got.Epoch != ev.Epoch || got.Key != ev.Key || got.ClientID != ev.ClientID {
		t.Fatalf("round-trip lost fields: %+v", got)
	}
	if got.Register == nil || !got.Register.Equal(*ev.Register) {
		t.Fatalf("round-trip lost register: %+v", got.Register)
	}
	if string(got.Data) != string(ev.Data) {
		t.Fatalf("round-trip lost data: %s", got.Data)
	}
	if len(got.State) != 1 || !got.State["title"].Equal(ev.State["title"]) {
		t.Fatalf("round-trip lost state: %+v", got.State)
	}
}

func TestHTTPClusterRelayPostsWireEvent(t *testing.T) {
	got := make(chan ClusterEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		ev, err := UnmarshalClusterEvent(mustReadAll(t, r))
		if err != nil {
			t.Fatalf("handler unmarshal failed: %v", err)
		}
		got <- ev
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	relay := NewHTTPClusterRelay([]string{srv.URL}, "/cluster/event", srv.Client())
	relay.Publish(ClusterEvent{Room: "demo", Kind: "presence", From: "node-a"})

	select {
	case ev := <-got:
		if ev.Room != "demo" || ev.Kind != "presence" || ev.From != "node-a" {
			t.Fatalf("unexpected event delivered: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not POST event to peer")
	}
}

func TestServeClusterEventPublishesToLocalBus(t *testing.T) {
	bus := NewMemoryClusterBus()
	h := NewHub(Config{Bus: bus})
	r, err := h.room("demo")
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		id:   "c1",
		room: r,
		send: make(chan []byte, 4),
		claims: TokenClaims{
			Caps: map[string]bool{CapRead: true},
		},
	}
	r.cmds <- command{kind: "join", client: client}
	<-client.send

	req := httptest.NewRequest(http.MethodPost, "/cluster/event", strings.NewReader(`{"room":"demo","kind":"op","from":"node-b","key":"title","register":{"v":"\"hello\"","t":{"c":1,"n":"node-b"}}}`))
	rr := httptest.NewRecorder()
	h.ServeClusterEvent(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rr.Code)
	}

	select {
	case msg := <-client.send:
		if !strings.Contains(string(msg), `"type":"op"`) {
			t.Fatalf("expected relayed op frame, got %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("cluster event did not reach local room")
	}
}

func mustReadAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return body
}

type recordingStore struct {
	mu       sync.Mutex
	appended map[string]crdt.Register
}

func (s *recordingStore) Load(string) (map[string]crdt.Register, error) {
	return map[string]crdt.Register{}, nil
}

func (s *recordingStore) Append(_ string, rec store.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appended[rec.Key] = rec.Reg
	return nil
}

func (s *recordingStore) Compact(string, map[string]crdt.Register) error { return nil }
func (s *recordingStore) Close() error                                   { return nil }

func TestClusterOpIsPersistedOnReceiver(t *testing.T) {
	bus := NewMemoryClusterBus()
	st := &recordingStore{appended: map[string]crdt.Register{}}
	h := NewHub(Config{Bus: bus, Store: st, NodeID: "node-b"})
	if _, err := h.room("demo"); err != nil {
		t.Fatal(err)
	}

	reg := serverReg("remote", 3, "node-a")
	bus.Publish(ClusterEvent{Room: "demo", Kind: "op", From: "node-a", Epoch: 1, Key: "title", Register: &reg})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		st.mu.Lock()
		_, ok := st.appended["title"]
		st.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cluster op was not persisted on the receiving node")
}

func TestClusterConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"single node", Config{NodeID: ""}, false},
		{"single node with secret", Config{NodeID: "", Secret: "s"}, false},
		{"cluster without node id", Config{ClusterPeers: []string{"http://peer"}}, true},
		{"cluster with default node id", Config{NodeID: "local", ClusterPeers: []string{"http://peer"}}, true},
		{"cluster with secret but no admin token", Config{NodeID: "a", Secret: "s", ClusterPeers: []string{"http://peer"}}, true},
		{"cluster valid", Config{NodeID: "a", AdminToken: "t", ClusterPeers: []string{"http://peer"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestServeClusterEventCreatesRoomWithoutBus(t *testing.T) {
	h := NewHub(Config{NodeID: "node-b"})
	body := `{"room":"fresh","kind":"op","from":"node-a","epoch":1,"key":"title","register":{"v":"hello","t":{"c":1,"n":"node-a"}}}`
	rr := httptest.NewRecorder()
	h.ServeClusterEvent(rr, httptest.NewRequest(http.MethodPost, "/cluster/event", strings.NewReader(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rr.Code, rr.Body.String())
	}

	r, err := h.room("fresh")
	if err != nil {
		t.Fatal(err)
	}
	reader := &Client{
		id:     "reader",
		room:   r,
		send:   make(chan []byte, 4),
		claims: TokenClaims{Caps: map[string]bool{CapRead: true}},
	}
	r.cmds <- command{kind: "join", client: reader}
	select {
	case msg := <-reader.send:
		var out Outbound
		if err := json.Unmarshal(msg, &out); err != nil {
			t.Fatal(err)
		}
		if out.Type != msgSnapshot {
			t.Fatalf("expected snapshot, got %q", out.Type)
		}
		reg, ok := out.State["title"]
		if !ok || string(reg.Value) != `"hello"` {
			t.Fatalf("relayed op missing from snapshot: %+v", out.State)
		}
	case <-time.After(time.Second):
		t.Fatal("cluster event did not reach the created room")
	}
}

func TestServeClusterEventIgnoresOwnEvents(t *testing.T) {
	h := NewHub(Config{NodeID: "node-a"})
	body := `{"room":"demo","kind":"op","from":"node-a","key":"title","register":{"v":"\"x\"","t":{"c":1,"n":"node-a"}}}`
	rr := httptest.NewRecorder()
	h.ServeClusterEvent(rr, httptest.NewRequest(http.MethodPost, "/cluster/event", strings.NewReader(body)))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected idempotent 202, got %d", rr.Code)
	}
	h.mu.Lock()
	rooms := len(h.rooms)
	h.mu.Unlock()
	if rooms != 0 {
		t.Fatalf("self-originated event created %d room(s)", rooms)
	}
}

// TestTwoNodeHTTPRelayConverges exercises the real path: node A writes, the
// relay POSTs to node B's /cluster/event, and B applies + persists the write.
func TestTwoNodeHTTPRelayConverges(t *testing.T) {
	stB := &recordingStore{appended: map[string]crdt.Register{}}

	var hubA, hubB *Hub
	handler := func(h **Hub) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			(*h).ServeClusterEvent(w, r)
		})
	}
	srvA := httptest.NewServer(handler(&hubA))
	defer srvA.Close()
	srvB := httptest.NewServer(handler(&hubB))
	defer srvB.Close()

	hubA = NewHub(Config{
		NodeID:       "node-a",
		AdminToken:   "cluster-secret",
		ClusterPeers: []string{srvB.URL},
		ClusterPath:  "/cluster/event",
		ClusterHTTP:  srvB.Client(),
	})
	hubB = NewHub(Config{
		NodeID:       "node-b",
		AdminToken:   "cluster-secret",
		ClusterPeers: []string{srvA.URL},
		ClusterPath:  "/cluster/event",
		ClusterHTTP:  srvA.Client(),
		Store:        stB,
	})

	rA, err := hubA.room("demo")
	if err != nil {
		t.Fatal(err)
	}
	rB, err := hubB.room("demo")
	if err != nil {
		t.Fatal(err)
	}

	reader := &Client{
		id:     "reader",
		room:   rB,
		send:   make(chan []byte, 8),
		claims: TokenClaims{Caps: map[string]bool{CapRead: true}},
	}
	rB.cmds <- command{kind: "join", client: reader}
	<-reader.send

	writer := &Client{
		id:     "writer",
		room:   rA,
		send:   make(chan []byte, 8),
		claims: TokenClaims{Caps: map[string]bool{CapRead: true, CapWrite: true}},
	}
	rA.cmds <- command{kind: "join", client: writer}
	<-writer.send

	rA.cmds <- command{
		kind:   "op",
		client: writer,
		in: Inbound{
			Type:     "op",
			Epoch:    rA.epoch,
			Key:      "title",
			Register: &crdt.Register{Value: []byte(`"from-a"`), TS: crdt.Timestamp{Counter: 5, Node: "client-a"}},
		},
	}

	select {
	case msg := <-reader.send:
		if !strings.Contains(string(msg), `"type":"op"`) || !strings.Contains(string(msg), `"title"`) {
			t.Fatalf("unexpected relayed frame: %s", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("node B did not receive the op through the HTTP relay")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stB.mu.Lock()
		reg, ok := stB.appended["title"]
		stB.mu.Unlock()
		if ok {
			if string(reg.Value) != `"from-a"` {
				t.Fatalf("persisted wrong value: %s", reg.Value)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("node B did not persist the relayed op")
}

// TestAntiEntropyHealsMissedEvents simulates a dropped relay event and proves
// the periodic full-state push reconciles it without any client action.
func TestAntiEntropyHealsMissedEvents(t *testing.T) {
	var hubB *Hub
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubB.ServeClusterEvent(w, r)
	}))
	defer srvB.Close()

	hubB = NewHub(Config{NodeID: "node-b", AdminToken: "tok"})
	hubA := NewHub(Config{
		NodeID:       "node-a",
		AdminToken:   "tok",
		ClusterPeers: []string{srvB.URL},
		ClusterPath:  "/cluster/event",
		ClusterHTTP:  srvB.Client(),
	})
	dropRelay := &togglableRelay{inner: hubA.relay}
	hubA.relay = dropRelay // used by antiEntropyOnce; rooms copy it at creation
	rA, err := hubA.room("demo")
	if err != nil {
		t.Fatal(err)
	}
	writer := &Client{
		id:     "writer",
		room:   rA,
		send:   make(chan []byte, 8),
		claims: TokenClaims{Caps: map[string]bool{CapRead: true, CapWrite: true}},
	}
	rA.cmds <- command{kind: "join", client: writer}
	<-writer.send

	// Drop every event from this write, as a network partition or a saturated
	// relay queue would.
	dropRelay.drop.Store(true)
	rA.cmds <- command{
		kind:   "op",
		client: writer,
		in: Inbound{
			Type:     "op",
			Epoch:    rA.epoch,
			Key:      "title",
			Register: &crdt.Register{Value: []byte(`"healed"`), TS: crdt.Timestamp{Counter: 9, Node: "client-a"}},
		},
	}
	<-writer.send // local broadcast

	hubB.mu.Lock()
	_, exists := hubB.rooms["demo"]
	hubB.mu.Unlock()
	if exists {
		t.Fatal("node B should not know about the room before anti-entropy")
	}

	// Restore the relay and run one anti-entropy pass.
	dropRelay.drop.Store(false)
	hubA.antiEntropyOnce()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats := hubB.Stats()
		if s, ok := stats["demo"]; ok && s.Live >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("anti-entropy did not reconcile the missed write")
}

// togglableRelay drops events while enabled, modelling a partition.
type togglableRelay struct {
	inner ClusterRelay
	drop  atomic.Bool
}

func (r *togglableRelay) Publish(ev ClusterEvent) {
	if r.drop.Load() {
		return
	}
	r.inner.Publish(ev)
}

func TestServeMetricsIncludesClusterState(t *testing.T) {
	relay := NewHTTPClusterRelay([]string{"http://127.0.0.1:1"}, "/cluster/event", nil)
	h := NewHub(Config{NodeID: "node-x"})
	h.relay = relay

	rr := httptest.NewRecorder()
	h.ServeMetrics(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`syncwell_cluster_node_info{node="node-x"} 1`,
		"syncwell_cluster_events_ingested_total 0",
		"syncwell_cluster_events_dropped_total 0",
		"syncwell_cluster_relay_posts_total 0",
		`syncwell_cluster_peer_up{peer="http://127.0.0.1:1"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q in:\n%s", want, body)
		}
	}
}
