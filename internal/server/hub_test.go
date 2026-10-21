package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"syncwell/internal/crdt"
	"syncwell/internal/store"
)

type failingStore struct{}

func (failingStore) Load(string) (map[string]crdt.Register, error) {
	return map[string]crdt.Register{}, nil
}

func (failingStore) Append(string, store.Record) error {
	return errors.New("disk unavailable")
}

func (failingStore) Compact(string, map[string]crdt.Register) error { return nil }
func (failingStore) Close() error                                   { return nil }

func serverReg(v string, c uint64, n string) crdt.Register {
	b, _ := json.Marshal(v)
	return crdt.Register{Value: b, TS: crdt.Timestamp{Counter: c, Node: n}}
}

func testRoom(st store.Store) *Room {
	return &Room{
		id:           "room",
		doc:          crdt.NewLWWMap(),
		clock:        crdt.NewClock("server:room"),
		clients:      map[*Client]bool{},
		acked:        map[*Client]uint64{},
		done:         make(chan struct{}),
		store:        st,
		compactEvery: defaultCompactEvery,
		limits:       Limits{}.withDefaults(),
	}
}

func TestApplyAndPersistRejectsUndurableWinningWrite(t *testing.T) {
	r := testRoom(failingStore{})
	if _, ok := r.applyAndPersist("title", serverReg("lost", 1, "A")); ok {
		t.Fatal("undurable winning write was accepted")
	}
	if _, ok := r.doc.Entry("title"); ok {
		t.Fatal("undurable winning write mutated room state")
	}
}

func TestApplyAndPersistAllowsStaleWriteWithoutPersistence(t *testing.T) {
	r := testRoom(failingStore{})
	r.doc.ApplyRegister("title", serverReg("current", 2, "A"))

	winner, ok := r.applyAndPersist("title", serverReg("stale", 1, "A"))
	if !ok {
		t.Fatal("stale write should not require persistence")
	}
	if !winner.Equal(serverReg("current", 2, "A")) {
		t.Fatalf("stale write changed winner: %+v", winner)
	}
	got, _ := r.doc.Entry("title")
	if !got.Equal(serverReg("current", 2, "A")) {
		t.Fatalf("stale write mutated state: %+v", got)
	}
}

func TestSlowClientEvictionRemovesAckBookkeeping(t *testing.T) {
	r := testRoom(nil)
	cl := &Client{id: "c1", send: make(chan []byte)}
	r.clients[cl] = true
	r.acked[cl] = 42

	r.deliver(cl, []byte(`{}`))

	if r.clients[cl] {
		t.Fatal("slow client remained in client set")
	}
	if _, ok := r.acked[cl]; ok {
		t.Fatal("slow client ack state remained after eviction")
	}
	if _, ok := <-cl.send; ok {
		t.Fatal("slow client send channel was not closed")
	}
}

func TestHubShutdownRejectsNewTrafficAndStopsRooms(t *testing.T) {
	h := NewHub(Config{})
	r, err := h.room("room")
	if err != nil {
		t.Fatal(err)
	}
	cl := &Client{id: "c1", room: r, send: make(chan []byte, 1)}
	r.cmds <- command{kind: "join", client: cl}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	if h.Ready() {
		t.Fatal("hub reported ready after shutdown")
	}
	select {
	case <-r.done:
	default:
		t.Fatal("room loop did not stop")
	}
	for {
		select {
		case _, ok := <-cl.send:
			if !ok {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("client send channel remained open after shutdown")
		}
	}
}

func TestRoomRejectsStaleEpochOnOp(t *testing.T) {
	r := newRoom("room", "", nil, 0, nil, defaultCompactEvery, 0, 0, Limits{}.withDefaults(), nil, nil, "")
	cl := &Client{
		id:   "c1",
		room: r,
		send: make(chan []byte, 2),
		claims: TokenClaims{
			Caps: map[string]bool{CapRead: true, CapWrite: true, CapPresence: true},
		},
	}

	r.cmds <- command{kind: "join", client: cl}
	select {
	case <-cl.send:
	case <-time.After(time.Second):
		t.Fatal("did not receive initial snapshot")
	}

	r.cmds <- command{
		kind:   "op",
		client: cl,
		in: Inbound{
			Type:     "op",
			Epoch:    r.epoch - 1,
			Key:      "title",
			Register: &crdt.Register{TS: crdt.Timestamp{Counter: 1, Node: "c1"}},
		},
	}

	select {
	case msg := <-cl.send:
		if !strings.Contains(string(msg), "stale epoch") {
			t.Fatalf("expected stale epoch error, got %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive stale epoch error")
	}
}

func TestHubMaxRoomsCapacity(t *testing.T) {
	h := NewHub(Config{MaxRooms: 1})
	if _, err := h.room("a"); err != nil {
		t.Fatalf("first room rejected: %v", err)
	}
	if _, err := h.room("b"); !errors.Is(err, errRoomCapacity) {
		t.Fatalf("expected errRoomCapacity, got %v", err)
	}
	// An existing room must still resolve at capacity.
	if _, err := h.room("a"); err != nil {
		t.Fatalf("existing room lookup failed at capacity: %v", err)
	}
}

func TestRoomIdleEviction(t *testing.T) {
	h := NewHub(Config{IdleRoomTTL: 20 * time.Millisecond})
	r, err := h.room("gone")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("empty room was not evicted after the idle TTL")
	}
	h.mu.Lock()
	_, still := h.rooms["gone"]
	h.mu.Unlock()
	if still {
		t.Fatal("evicted room remained in the hub map")
	}
}

func TestRoomRejectsNodeMismatchOnOp(t *testing.T) {
	r := newRoom("room", "", nil, 0, nil, defaultCompactEvery, 0, 0, Limits{}.withDefaults(), nil, nil, "")
	cl := &Client{
		id:   "c1",
		room: r,
		send: make(chan []byte, 4),
		claims: TokenClaims{
			Caps: map[string]bool{CapRead: true, CapWrite: true, CapPresence: true},
		},
	}
	r.cmds <- command{kind: "join", client: cl}
	select {
	case <-cl.send: // initial snapshot
	case <-time.After(time.Second):
		t.Fatal("did not receive initial snapshot")
	}

	reg := crdt.Register{TS: crdt.Timestamp{Counter: 1, Node: "node-a"}}
	r.cmds <- command{kind: "op", client: cl, in: Inbound{Type: "op", Epoch: r.epoch, Key: "title", Register: &reg}}
	select {
	case <-cl.send: // broadcast of the accepted op
	case <-time.After(time.Second):
		t.Fatal("did not receive op broadcast")
	}

	bad := crdt.Register{TS: crdt.Timestamp{Counter: 2, Node: "node-b"}}
	r.cmds <- command{kind: "op", client: cl, in: Inbound{Type: "op", Epoch: r.epoch, Key: "title", Register: &bad}}
	select {
	case msg := <-cl.send:
		if !strings.Contains(string(msg), "node id mismatch") {
			t.Fatalf("expected node mismatch error, got %s", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive node mismatch error")
	}
}

func TestRoomRejectsStaleEpochOnMergeAndPresence(t *testing.T) {
	cases := []struct {
		name string
		kind string
		in   Inbound
	}{
		{
			name: "merge",
			kind: "merge",
			in: Inbound{
				Type:  "merge",
				Epoch: 0,
				State: map[string]crdt.Register{
					"title": {TS: crdt.Timestamp{Counter: 1, Node: "c1"}},
				},
			},
		},
		{
			name: "presence",
			kind: "presence",
			in: Inbound{
				Type: "presence",
				Data: json.RawMessage(`{"cursor":1}`),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRoom("room", "", nil, 0, nil, defaultCompactEvery, 0, 0, Limits{}.withDefaults(), nil, nil, "")
			cl := &Client{
				id:   "c1",
				room: r,
				send: make(chan []byte, 3),
				claims: TokenClaims{
					Caps: map[string]bool{CapRead: true, CapWrite: true, CapPresence: true},
				},
			}

			r.cmds <- command{kind: "join", client: cl}
			select {
			case <-cl.send:
			case <-time.After(time.Second):
				t.Fatal("did not receive initial snapshot")
			}

			tc.in.Epoch = r.epoch - 1
			r.cmds <- command{kind: tc.kind, client: cl, in: tc.in}

			select {
			case msg := <-cl.send:
				if !strings.Contains(string(msg), "stale epoch") {
					t.Fatalf("expected stale epoch error, got %s", msg)
				}
			case <-time.After(time.Second):
				t.Fatal("did not receive stale epoch error")
			}
		})
	}
}

type sizedStore struct {
	appended  int
	compacted int
	size      int64
}

func (s *sizedStore) Load(string) (map[string]crdt.Register, error) {
	return map[string]crdt.Register{}, nil
}
func (s *sizedStore) Append(string, store.Record) error {
	s.appended++
	s.size += 100
	return nil
}
func (s *sizedStore) Compact(string, map[string]crdt.Register) error {
	s.compacted++
	s.size = 64
	return nil
}
func (s *sizedStore) Close() error         { return nil }
func (s *sizedStore) LogSize(string) int64 { return s.size }

func TestRoomCompactsWhenLogExceedsCap(t *testing.T) {
	st := &sizedStore{}
	r := testRoom(st)
	r.compactEvery = 1 << 30
	r.maxLogBytes = 200

	r.applyAndPersist("k", serverReg("a", 1, "n"))
	if st.compacted != 0 {
		t.Fatalf("compacted below cap: %d", st.compacted)
	}
	r.applyAndPersist("k", serverReg("b", 2, "n"))
	if st.compacted != 1 {
		t.Fatalf("log at cap was not compacted: %d", st.compacted)
	}
	if r.opsSinceCompact != 0 {
		t.Fatalf("opsSinceCompact = %d after compaction, want 0", r.opsSinceCompact)
	}
}

func readOutbound(t *testing.T, cl *Client) Outbound {
	t.Helper()
	select {
	case raw, ok := <-cl.send:
		if !ok {
			t.Fatal("client channel closed")
		}
		var msg Outbound
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("bad outbound frame: %v", err)
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outbound frame")
		return Outbound{}
	}
}

func TestHelloSendsOnlyRegistersNewerThanVector(t *testing.T) {
	r := newRoom("room", "", nil, 0, nil, defaultCompactEvery, 0, 0, Limits{}.withDefaults(), nil, nil, "")
	r.doc.ApplyRegister("a", serverReg("1", 1, "A"))

	full := &Client{
		id:     "full",
		room:   r,
		send:   make(chan []byte, 8),
		claims: TokenClaims{Caps: map[string]bool{CapRead: true, CapWrite: true}},
	}
	r.cmds <- command{kind: "join", client: full}
	if msg := readOutbound(t, full); msg.Type != msgSnapshot {
		t.Fatalf("legacy client expected snapshot, got %q", msg.Type)
	}

	delta := &Client{
		id:     "delta",
		room:   r,
		send:   make(chan []byte, 8),
		claims: TokenClaims{Caps: map[string]bool{CapRead: true, CapWrite: true}},
		delta:  true,
	}
	r.cmds <- command{kind: "join", client: delta}
	select {
	case raw := <-delta.send:
		t.Fatalf("delta client should wait for hello, got %s", raw)
	case <-time.After(25 * time.Millisecond):
	}
	r.cmds <- command{kind: "hello", client: delta, in: Inbound{Type: "hello", Epoch: 1, Vector: map[string]crdt.Timestamp{}}}
	msg := readOutbound(t, delta)
	if msg.Type != msgDelta || len(msg.State) != 1 {
		t.Fatalf("expected one-key delta, got %+v", msg)
	}
	if _, ok := msg.State["a"]; !ok {
		t.Fatal("delta omitted a register the client lacks")
	}

	// A second hello that already knows the register gets an empty delta.
	r.cmds <- command{kind: "hello", client: delta, in: Inbound{Type: "hello", Epoch: 1, Vector: map[string]crdt.Timestamp{
		"a": {Counter: 1, Node: "A"},
	}}}
	msg = readOutbound(t, delta)
	if msg.Type != msgDelta || len(msg.State) != 0 {
		t.Fatalf("expected empty delta, got %+v", msg)
	}
}
