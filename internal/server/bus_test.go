package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
