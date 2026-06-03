package server

import (
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"sync/atomic"

	"syncwell/internal/crdt"
	"syncwell/internal/store"
)

// defaultCompactEvery is how many winning writes trigger a maintenance pass
// (tombstone GC + snapshot compaction). Configurable via NewHub.
const defaultCompactEvery = 256

// Wire message type tags (server -> client).
const (
	msgSnapshot = "snapshot" // full document state, sent once on join
	msgOp       = "op"       // a single committed register change, broadcast to all
	msgPresence = "presence" // ephemeral, non-persisted relay (cursors, who's-online)
	msgLeave    = "leave"    // a client disconnected
	msgError    = "error"    // server rejected the connection (e.g. room full)
)

// Inbound is a message from a client. As of Phase 2 the client authors the
// timestamp (client-side CRDT), so an `op` carries a full register rather than
// a bare value, and a `merge` carries the client's full state on (re)connect.
type Inbound struct {
	Type     string                   `json:"type"`               // "op" | "merge" | "presence" | "ack"
	Key      string                   `json:"key,omitempty"`      // for op
	Register *crdt.Register           `json:"register,omitempty"` // for op
	State    map[string]crdt.Register `json:"state,omitempty"`    // for merge
	Data     json.RawMessage          `json:"data,omitempty"`     // for presence
	Clock    uint64                   `json:"clock,omitempty"`    // for ack: client's current clock counter
}

// RoomStats is a point-in-time view of a room, exposed at /stats.
type RoomStats struct {
	Clients    int    `json:"clients"`
	Live       int    `json:"live"`
	Tombstones int    `json:"tombstones"`
	LowWater   uint64 `json:"lowWater"`
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
	kind   string // "join" | "leave" | "op" | "merge" | "presence" | "ack" | "stats"
	client *Client
	in     Inbound
	reply  chan RoomStats // for "stats"
}

// Room is one collaborative document: a CRDT plus the set of connected clients.
type Room struct {
	id              string
	doc             *crdt.LWWMap
	clock           *crdt.Clock
	clients         map[*Client]bool
	acked           map[*Client]uint64 // last clock counter each client has acknowledged
	cmds            chan command
	store           store.Store
	compactEvery    int
	maxClients      int
	opsSinceCompact int
}

func newRoom(id string, st store.Store, compactEvery, maxClients int) *Room {
	r := &Room{
		id:           id,
		doc:          crdt.NewLWWMap(),
		clock:        crdt.NewClock("server:" + id),
		clients:      map[*Client]bool{},
		acked:        map[*Client]uint64{},
		cmds:         make(chan command, 64),
		store:        st,
		compactEvery: compactEvery,
		maxClients:   maxClients,
	}
	// Restore persisted state so a restart loses nothing.
	if st != nil {
		if snap, err := st.Load(id); err != nil {
			log.Printf("room %s: load: %v", id, err)
		} else {
			r.doc.Load(snap)
			for _, reg := range snap {
				r.clock.Observe(reg.TS) // never re-issue a timestamp we've already used
			}
		}
	}
	go r.loop()
	return r
}

// applyAndPersist merges an incoming register, durably records it if it won,
// triggers periodic maintenance, and returns the current (winning) register.
func (r *Room) applyAndPersist(key string, reg crdt.Register) crdt.Register {
	r.clock.Observe(reg.TS)
	r.doc.ApplyRegister(key, reg)
	cur, _ := r.doc.Entry(key)
	if cur.TS == reg.TS { // the incoming write won
		if r.store != nil {
			if err := r.store.Append(r.id, store.Record{Key: key, Reg: cur}); err != nil {
				log.Printf("room %s: persist: %v", r.id, err)
			}
		}
		if r.opsSinceCompact++; r.opsSinceCompact >= r.compactEvery {
			r.maintain()
			r.opsSinceCompact = 0
		}
	}
	return cur
}

// maintain runs the periodic housekeeping: garbage-collect tombstones that are
// causally stable, then (if persistent) compact the op-log to a snapshot. GC and
// compaction run regardless of persistence so memory stays bounded in-memory too.
func (r *Room) maintain() {
	if lw := r.lowWater(); lw > 0 {
		if n := r.doc.CollectTombstones(lw); n > 0 {
			log.Printf("room %s: gc collected %d tombstones below low-water %d", r.id, n, lw)
		}
	}
	if r.store != nil {
		if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
			log.Printf("room %s: compact: %v", r.id, err)
		}
	}
}

// noteClock records the highest logical clock value a client has revealed.
func (r *Room) noteClock(cl *Client, counter uint64) {
	if counter > r.acked[cl] {
		r.acked[cl] = counter
	}
}

// lowWater is the causal-stability bound: the minimum clock counter across all
// connected clients. No future write from a connected client can fall below it
// (Lamport counters only increase), so tombstones older than it are safe to GC.
// Returns 0 (GC disabled) whenever a client has not yet revealed its clock, which
// keeps the bound conservative while clients are catching up. The known limit:
// a client offline longer than a GC cycle is not counted — see DESIGN.md §4.
func (r *Room) lowWater() uint64 {
	if len(r.acked) == 0 {
		return 0
	}
	min := ^uint64(0)
	for _, c := range r.acked {
		if c < min {
			min = c
		}
	}
	return min
}

// loop is the room's single owner goroutine. It is the *only* place that reads
// or writes r.doc, r.clock, and r.clients, which is what makes the lock-free
// design correct.
func (r *Room) loop() {
	for c := range r.cmds {
		switch c.kind {
		case "join":
			if r.maxClients > 0 && len(r.clients) >= r.maxClients {
				// Reject over-capacity: tell the client, then close its channel.
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"room full"`)})
				close(c.client.send)
				continue
			}
			r.clients[c.client] = true
			r.acked[c.client] = 0
			r.sendTo(c.client, Outbound{Type: msgSnapshot, State: r.doc.Snapshot()})

		case "leave":
			if _, ok := r.clients[c.client]; ok {
				delete(r.clients, c.client)
				delete(r.acked, c.client)
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
				r.noteClock(c.client, c.in.Register.TS.Counter)
				winner := r.applyAndPersist(c.in.Key, *c.in.Register)
				r.broadcast(Outbound{Type: msgOp, Key: c.in.Key, Register: &winner}, nil)
			}

		case "merge":
			// A (re)connecting client ships its full local state — this is how
			// edits made while offline propagate. Merge each register, broadcast
			// the winners to the other clients, then send the merging client a
			// fresh snapshot so it converges with everything it missed.
			var maxCounter uint64
			for k, reg := range c.in.State {
				if reg.TS.Counter > maxCounter {
					maxCounter = reg.TS.Counter
				}
				winner := r.applyAndPersist(k, reg)
				r.broadcast(Outbound{Type: msgOp, Key: k, Register: &winner}, c.client)
			}
			r.noteClock(c.client, maxCounter)
			r.sendTo(c.client, Outbound{Type: msgSnapshot, State: r.doc.Snapshot()})

		case "ack":
			// Idle clients advertise their clock so the GC low-water mark can
			// advance even when they aren't writing.
			r.noteClock(c.client, c.in.Clock)

		case "presence":
			r.broadcast(Outbound{Type: msgPresence, ClientID: c.client.id, Data: c.in.Data}, c.client)

		case "stats":
			s := r.doc.Stats()
			c.reply <- RoomStats{Clients: len(r.clients), Live: s.Live, Tombstones: s.Tombstones, LowWater: r.lowWater()}
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

// Config configures a Hub. The zero value is a valid, fully-open dev server.
type Config struct {
	Store        store.Store // nil = in-memory only
	CompactEvery int         // <=0 = default
	Secret       string      // "" = auth disabled (open)
	Origins      []string    // empty = allow any Origin
	MaxClients   int         // 0 = unlimited per room
}

// Hub multiplexes rooms by id, creating them lazily on first join.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]*Room
	cfg   Config
}

// NewHub returns a hub for the given config.
func NewHub(cfg Config) *Hub {
	if cfg.CompactEvery <= 0 {
		cfg.CompactEvery = defaultCompactEvery
	}
	return &Hub{rooms: map[string]*Room{}, cfg: cfg}
}

func (h *Hub) room(id string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[id]
	if !ok {
		r = newRoom(id, h.cfg.Store, h.cfg.CompactEvery, h.cfg.MaxClients)
		h.rooms[id] = r
	}
	return r
}

// authorize enforces origin allow-listing and room-token checks before upgrade.
func (h *Hub) originAllowed(origin string) bool {
	if len(h.cfg.Origins) == 0 || origin == "" {
		return true
	}
	for _, o := range h.cfg.Origins {
		if o == origin {
			return true
		}
	}
	return false
}

// Stats queries every room's owner goroutine and returns a snapshot map. Safe to
// call from an HTTP handler — it communicates with rooms by message, not shared
// memory.
func (h *Hub) Stats() map[string]RoomStats {
	h.mu.Lock()
	ids := make([]string, 0, len(h.rooms))
	rooms := make([]*Room, 0, len(h.rooms))
	for id, r := range h.rooms {
		ids = append(ids, id)
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	out := make(map[string]RoomStats, len(rooms))
	for i, r := range rooms {
		reply := make(chan RoomStats, 1)
		r.cmds <- command{kind: "stats", reply: reply}
		out[ids[i]] = <-reply
	}
	return out
}

var clientSeq uint64

func nextClientID() string {
	return "c" + strconv.FormatUint(atomic.AddUint64(&clientSeq, 1), 10)
}
