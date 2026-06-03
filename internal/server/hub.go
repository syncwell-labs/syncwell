package server

import (
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"sync/atomic"

	"syncwell/internal/crdt"
)

// Wire message type tags (server -> client).
const (
	msgSnapshot = "snapshot" // full document state, sent once on join
	msgOp       = "op"       // a single committed register change, broadcast to all
	msgPresence = "presence" // ephemeral, non-persisted relay (cursors, who's-online)
	msgLeave    = "leave"    // a client disconnected
)

// Inbound is a message from a client. As of Phase 2 the client authors the
// timestamp (client-side CRDT), so an `op` carries a full register rather than
// a bare value, and a `merge` carries the client's full state on (re)connect.
type Inbound struct {
	Type     string                   `json:"type"`             // "op" | "merge" | "presence"
	Key      string                   `json:"key,omitempty"`    // for op
	Register *crdt.Register           `json:"register,omitempty"` // for op
	State    map[string]crdt.Register `json:"state,omitempty"`  // for merge
	Data     json.RawMessage          `json:"data,omitempty"`   // for presence
}

// Outbound is a message to a client.
type Outbound struct {
	Type     string                   `json:"type"`
	State    map[string]crdt.Register `json:"state,omitempty"`
	Key      string                   `json:"key,omitempty"`
	Register *crdt.Register           `json:"register,omitempty"`
	ClientID string                   `json:"clientId,omitempty"`
	Data     json.RawMessage          `json:"data,omitempty"`
}

// Client is one websocket connection within a Room. Its send channel is owned
// (and only ever closed) by the room goroutine — see the single-owner model.
type Client struct {
	id   string
	room *Room
	send chan []byte
}

// command is an instruction handed to a room's single-owner goroutine. All
// mutation of room state happens inside that goroutine, so the document needs
// no locks — concurrency is structured as message passing, not shared memory.
type command struct {
	kind   string // "join" | "leave" | "op" | "presence"
	client *Client
	in     Inbound
}

// Room is one collaborative document: a CRDT plus the set of connected clients.
type Room struct {
	id      string
	doc     *crdt.LWWMap
	clock   *crdt.Clock
	clients map[*Client]bool
	cmds    chan command
}

func newRoom(id string) *Room {
	r := &Room{
		id:      id,
		doc:     crdt.NewLWWMap(),
		clock:   crdt.NewClock("server:" + id),
		clients: map[*Client]bool{},
		cmds:    make(chan command, 64),
	}
	go r.loop()
	return r
}

// loop is the room's single owner goroutine. It is the *only* place that reads
// or writes r.doc, r.clock, and r.clients, which is what makes the lock-free
// design correct.
func (r *Room) loop() {
	for c := range r.cmds {
		switch c.kind {
		case "join":
			r.clients[c.client] = true
			r.sendTo(c.client, Outbound{Type: msgSnapshot, State: r.doc.Snapshot()})

		case "leave":
			if _, ok := r.clients[c.client]; ok {
				delete(r.clients, c.client)
				close(c.client.send)
				r.broadcast(Outbound{Type: msgLeave, ClientID: c.client.id}, nil)
			}

		case "op":
			// Client-authoritative: the write already carries the client's
			// logical timestamp. The server merges it (CRDT join), advances its
			// own clock so future server-side stamps stay causal, and broadcasts
			// the winning register to everyone — including the sender, which is
			// harmless because applying it is idempotent.
			if c.in.Register != nil {
				r.clock.Observe(c.in.Register.TS)
				r.doc.ApplyRegister(c.in.Key, *c.in.Register)
				reg, _ := r.doc.Entry(c.in.Key)
				r.broadcast(Outbound{Type: msgOp, Key: c.in.Key, Register: &reg}, nil)
			}

		case "merge":
			// A (re)connecting client ships its full local state — this is how
			// edits made while offline propagate. Merge each register, broadcast
			// the winners to the other clients, then send the merging client a
			// fresh snapshot so it converges with everything it missed.
			for k, reg := range c.in.State {
				r.clock.Observe(reg.TS)
				r.doc.ApplyRegister(k, reg)
			}
			for k := range c.in.State {
				reg, _ := r.doc.Entry(k)
				winner := reg
				r.broadcast(Outbound{Type: msgOp, Key: k, Register: &winner}, c.client)
			}
			r.sendTo(c.client, Outbound{Type: msgSnapshot, State: r.doc.Snapshot()})

		case "presence":
			r.broadcast(Outbound{Type: msgPresence, ClientID: c.client.id, Data: c.in.Data}, c.client)
		}
	}
}

// broadcast sends to every client except `except`. A client whose buffer is
// full is dropped (back-pressure protection): one slow consumer must not stall
// the whole room.
func (r *Room) broadcast(o Outbound, except *Client) {
	b, err := json.Marshal(o)
	if err != nil {
		log.Printf("room %s: marshal: %v", r.id, err)
		return
	}
	for cl := range r.clients {
		if cl == except {
			continue
		}
		r.deliver(cl, b)
	}
}

func (r *Room) sendTo(cl *Client, o Outbound) {
	b, err := json.Marshal(o)
	if err != nil {
		return
	}
	r.deliver(cl, b)
}

func (r *Room) deliver(cl *Client, b []byte) {
	select {
	case cl.send <- b:
	default:
		// Slow/stuck client: evict it. Safe to delete during range in Go.
		close(cl.send)
		delete(r.clients, cl)
	}
}

// Hub multiplexes rooms by id, creating them lazily on first join.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{rooms: map[string]*Room{}} }

func (h *Hub) room(id string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[id]
	if !ok {
		r = newRoom(id)
		h.rooms[id] = r
	}
	return r
}

var clientSeq uint64

func nextClientID() string {
	return "c" + strconv.FormatUint(atomic.AddUint64(&clientSeq, 1), 10)
}
