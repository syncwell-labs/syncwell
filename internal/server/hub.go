package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"syncwell/internal/crdt"
	"syncwell/internal/store"
)

// defaultIdleRoomTTL is how long an empty room is kept before it is evicted,
// giving reconnecting clients a grace window without leaking rooms forever.
const defaultIdleRoomTTL = 60 * time.Second

// errRoomCapacity is returned when the configured maximum number of live rooms
// would be exceeded by creating another one.
var errRoomCapacity = errors.New("room capacity reached")

// defaultCompactEvery is how many winning writes trigger a maintenance pass
// (tombstone GC + snapshot compaction). Configurable via NewHub.
const defaultCompactEvery = 256

// Wire message type tags (server -> client).
const (
	msgSnapshot = "snapshot" // full document state, sent once on join
	msgDelta    = "delta"    // state-vector diff for a returning client
	msgOp       = "op"       // a single committed register change, broadcast to all
	msgPresence = "presence" // ephemeral, non-persisted relay (cursors, who's-online)
	msgLeave    = "leave"    // a client disconnected
	msgError    = "error"    // server rejected the connection (e.g. room full)
)

const protocolVersion = 1

// Inbound is a message from a client. As of Phase 2 the client authors the
// timestamp (client-side CRDT), so an `op` carries a full register rather than
// a bare value, and a `merge` carries the client's full state on (re)connect.
type Inbound struct {
	Protocol int                       `json:"protocol,omitempty"`
	Epoch    uint64                    `json:"epoch,omitempty"`
	Type     string                    `json:"type"`               // "op" | "merge" | "hello" | "presence" | "ack"
	Key      string                    `json:"key,omitempty"`      // for op
	Register *crdt.Register            `json:"register,omitempty"` // for op
	State    map[string]crdt.Register  `json:"state,omitempty"`    // for merge/hello
	Vector   map[string]crdt.Timestamp `json:"vector,omitempty"`   // for hello: client's state vector
	Data     json.RawMessage           `json:"data,omitempty"`     // for presence
	Clock    uint64                    `json:"clock,omitempty"`    // for ack: client's current clock counter
}

// RoomStats is a point-in-time view of a room, exposed at /stats.
type RoomStats struct {
	Clients    int    `json:"clients"`
	Live       int    `json:"live"`
	Tombstones int    `json:"tombstones"`
	LowWater   uint64 `json:"lowWater"`
	Owner      string `json:"owner,omitempty"`
}

// Outbound is a message to a client.
type Outbound struct {
	Protocol int                      `json:"protocol,omitempty"`
	Epoch    uint64                   `json:"epoch,omitempty"`
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
	id     string
	room   *Room
	send   chan []byte
	claims TokenClaims
	node   string // authoring node id; pinned to prevent mid-stream impersonation
	delta  bool   // client requested state-vector delta sync on join
}

// acceptsNode binds the connection to a single authoring node on first use and
// rejects later registers that claim a different node id.
func (c *Client) acceptsNode(node string) bool {
	if node == "" {
		return false
	}
	if c.node == "" {
		c.node = node
		return true
	}
	return c.node == node
}

func (c *Client) canRead() bool {
	return c.claims.Can(CapRead)
}

func (c *Client) canWrite() bool {
	return c.claims.Can(CapWrite)
}

func (c *Client) canPresence() bool {
	return c.claims.Can(CapPresence)
}

// command is an instruction handed to a room's single-owner goroutine. All
// mutation of room state happens inside that goroutine, so the document needs
// no locks — concurrency is structured as message passing, not shared memory.
type command struct {
	kind    string // "join" | "leave" | "op" | "merge" | "presence" | "ack" | "stats"
	client  *Client
	in      Inbound
	cluster *ClusterEvent
	reply   chan RoomStats                // for "stats"
	export  chan map[string]crdt.Register // for "export"
	result  chan bool                     // for admin actions
	done    chan struct{}                 // for "shutdown"
}

// Room is one collaborative document: a CRDT plus the set of connected clients.
type Room struct {
	id              string
	owner           string
	hub             *Hub
	bus             ClusterBus
	relay           ClusterRelay
	nodeID          string
	unsubscribe     func()
	doc             *crdt.LWWMap
	clock           *crdt.Clock
	epoch           uint64
	clients         map[*Client]bool
	acked           map[*Client]uint64 // last clock counter each client has acknowledged
	sent            map[*Client]uint64 // highest counter the server has sent each client
	cmds            chan command
	done            chan struct{}
	store           store.Store
	compactEvery    int
	maxClients      int
	maxLogBytes     int64
	limits          Limits
	opsSinceCompact int
	persisted       bool
	idleTTL         time.Duration
	idleTimer       *time.Timer
}

func newRoom(id, owner string, hub *Hub, idleTTL time.Duration, st store.Store, compactEvery, maxClients int, maxLogBytes int64, limits Limits, bus ClusterBus, relay ClusterRelay, nodeID string) *Room {
	r := &Room{
		id:           id,
		owner:        owner,
		hub:          hub,
		bus:          bus,
		relay:        relay,
		nodeID:       nodeID,
		doc:          crdt.NewLWWMap(),
		clock:        crdt.NewClock("server:" + id),
		epoch:        1,
		clients:      map[*Client]bool{},
		acked:        map[*Client]uint64{},
		sent:         map[*Client]uint64{},
		cmds:         make(chan command, 64),
		done:         make(chan struct{}),
		store:        st,
		compactEvery: compactEvery,
		maxClients:   maxClients,
		maxLogBytes:  maxLogBytes,
		limits:       limits,
		idleTTL:      idleTTL,
	}
	if bus != nil {
		ch, unsubscribe := bus.Subscribe(id)
		r.unsubscribe = unsubscribe
		go func() {
			for {
				select {
				case <-r.done:
					return
				case ev := <-ch:
					select {
					case <-r.done:
						return
					case r.cmds <- command{kind: "cluster", cluster: &ev}:
					}
				}
			}
		}()
	}
	// Persisted state is restored inside the room goroutine (loadPersisted), not
	// here: newRoom is called under the hub mutex, and disk I/O must not block
	// every other room's lookup.
	r.armIdle()
	go r.loop()
	return r
}

// applyAndPersist merges an incoming register, durably records it if it won,
// triggers periodic maintenance, and returns the current (winning) register.
func (r *Room) applyAndPersist(key string, reg crdt.Register) (crdt.Register, bool) {
	_, span := otel.Tracer("syncwell").Start(context.Background(), "crdt.apply",
		trace.WithAttributes(
			attribute.String("syncwell.room", r.id),
			attribute.String("syncwell.key", key),
			attribute.String("syncwell.node", reg.TS.Node),
		),
	)
	defer span.End()
	started := time.Now()
	if r.hub != nil && r.hub.metrics != nil {
		defer func() {
			r.hub.metrics.applyDuration.observe(time.Since(started).Seconds())
		}()
	}
	r.clock.Observe(reg.TS)
	cur, ok := r.doc.Entry(key)
	winner := reg
	if ok {
		winner = crdt.ResolveRegister(cur, reg)
	}
	if ok && cur.Equal(winner) {
		return winner, true
	}
	if winner.Equal(reg) { // the incoming write won
		if r.store != nil {
			if err := r.store.Append(r.id, store.Record{Key: key, Reg: winner}); err != nil {
				span.SetStatus(codes.Error, "persist failed")
				LogEvent("room_persist_error", map[string]any{"room": r.id, "error": err.Error(), "key": key})
				return cur, false
			}
		}
		r.doc.ApplyRegister(key, reg)
		r.opsSinceCompact++
		if r.opsSinceCompact >= r.compactEvery || r.logFull() {
			r.maintain()
			r.opsSinceCompact = 0
		}
		return winner, true
	}
	return winner, true
}

// logSizer is implemented by stores that can report their append-log size.
type logSizer interface {
	LogSize(room string) int64
}

// logFull reports whether the room's append log has reached the configured cap
// and should be compacted on the next maintenance pass.
func (r *Room) logFull() bool {
	if r.maxLogBytes <= 0 || r.store == nil {
		return false
	}
	sizer, ok := r.store.(logSizer)
	if !ok {
		return false
	}
	return sizer.LogSize(r.id) >= r.maxLogBytes
}

// maintain runs the periodic housekeeping: garbage-collect tombstones that are
// causally stable, then (if persistent) compact the op-log to a snapshot. GC and
// compaction run regardless of persistence so memory stays bounded in-memory too.
func (r *Room) maintain() {
	// Tombstone collection is a server-memory optimisation; it must not change
	// the visible document or the epoch. Bumping the epoch here would reject
	// in-flight ops from connected clients and force a reconnect for no
	// observable benefit (clients keep their own tombstones harmlessly).
	if lw := r.lowWater(); lw > 0 {
		if n := r.doc.CollectTombstones(lw); n > 0 {
			LogEvent("room_gc", map[string]any{"room": r.id, "tombstones": n, "low_water": lw})
		}
	}
	if r.store != nil {
		if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
			LogEvent("room_compact_error", map[string]any{"room": r.id, "error": err.Error()})
		} else if !r.persisted {
			r.persisted = true
			r.hub.emitWebhook("room.persisted", r.id, map[string]any{"registers": len(r.doc.Snapshot())})
		}
	}
}

// emitWebhook forwards a room milestone without blocking the room goroutine.
func (h *Hub) emitWebhook(event, room string, data map[string]any) {
	if h == nil || h.webhooks == nil {
		return
	}
	h.webhooks.Emit(event, room, h.nodeID, data)
}

// noteClock records the highest logical clock value a client has revealed,
// clamped to what the server has actually sent that client. This keeps the GC
// low-water a true causal bound instead of trusting an arbitrary client claim.
func (r *Room) noteClock(cl *Client, counter uint64) {
	if counter > r.sent[cl] {
		counter = r.sent[cl]
	}
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

// loadPersisted restores the room document from the store. It runs inside the
// room goroutine so a large op-log replay never holds the global hub mutex.
func (r *Room) loadPersisted() {
	if r.store == nil {
		return
	}
	snap, err := r.store.Load(r.id)
	if err != nil {
		LogEvent("room_load_error", map[string]any{"room": r.id, "error": err.Error()})
		return
	}
	r.doc.Load(snap)
	for _, reg := range snap {
		r.clock.Observe(reg.TS) // never re-issue a timestamp we've already used
	}
}

// armIdle schedules eviction of an empty room after the idle TTL.
func (r *Room) armIdle() {
	if r.idleTTL <= 0 {
		return
	}
	if r.idleTimer != nil {
		r.idleTimer.Stop()
	}
	r.idleTimer = time.AfterFunc(r.idleTTL, func() {
		select {
		case <-r.done:
		case r.cmds <- command{kind: "evict"}:
		}
	})
}

// disarmIdle cancels a pending idle eviction (a client joined).
func (r *Room) disarmIdle() {
	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
}

// evictIfEmpty tears the room down when it is still empty. Only called from the
// room goroutine.
func (r *Room) evictIfEmpty() bool {
	// A join may already be queued behind this evict command; treat pending
	// work as "not idle" and re-arm rather than dropping a live connection.
	if len(r.clients) > 0 || len(r.cmds) > 0 {
		r.armIdle()
		return false
	}
	r.disarmIdle()
	if r.store != nil {
		if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
			LogEvent("room_compact_error", map[string]any{"room": r.id, "error": err.Error()})
		}
	}
	if r.unsubscribe != nil {
		r.unsubscribe()
		r.unsubscribe = nil
	}
	if r.hub != nil {
		r.hub.removeRoom(r)
		r.hub.emitWebhook("room.evicted", r.id, map[string]any{"idle_ttl": r.idleTTL.String()})
	}
	LogEvent("room_evicted", map[string]any{"room": r.id, "idle_ttl": r.idleTTL.String()})
	return true
}

// forceEvict tears the room down even if clients are connected. It is used by
// the admin dashboard for operator-initiated eviction (for example, before a
// rolling restart) and broadcasts a close to every attached client.
func (r *Room) forceEvict() {
	for cl := range r.clients {
		close(cl.send)
		delete(r.clients, cl)
		delete(r.acked, cl)
		delete(r.sent, cl)
	}
	r.disarmIdle()
	if r.store != nil {
		if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
			LogEvent("room_compact_error", map[string]any{"room": r.id, "error": err.Error()})
		}
	}
	if r.unsubscribe != nil {
		r.unsubscribe()
		r.unsubscribe = nil
	}
	if r.hub != nil {
		r.hub.removeRoom(r)
		r.hub.emitWebhook("room.evicted", r.id, map[string]any{"forced": true})
	}
	LogEvent("room_evicted", map[string]any{"room": r.id, "forced": true})
}

// loop is the room's single owner goroutine. It is the *only* place that reads
// or writes r.doc, r.clock, and r.clients, which is what makes the lock-free
// design correct.
func (r *Room) loop() {
	defer close(r.done)
	r.loadPersisted()
	for c := range r.cmds {
		switch c.kind {
		case "join":
			r.disarmIdle()
			if r.maxClients > 0 && len(r.clients) >= r.maxClients {
				// Reject over-capacity: tell the client, then close its channel.
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"room full"`)})
				close(c.client.send)
				continue
			}
			r.clients[c.client] = true
			r.acked[c.client] = 0
			r.sent[c.client] = 0
			if c.client.delta {
				// Wait for the client's state vector, then send only the
				// registers it is missing instead of a full snapshot.
				continue
			}
			r.sendTo(c.client, Outbound{Type: msgSnapshot, State: r.doc.Snapshot(), Epoch: r.epoch})

		case "leave":
			if r.removeClient(c.client) {
				r.broadcast(Outbound{Type: msgLeave, ClientID: c.client.id}, nil)
			}

		case "op":
			if !c.client.canWrite() {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"write capability required"`)})
				continue
			}
			if c.in.Epoch != r.epoch {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"stale epoch"`)})
				continue
			}
			// Client-authoritative: the write already carries the client's
			// logical timestamp. The server merges it (CRDT join), advances its
			// own clock so future server-side stamps stay causal, and broadcasts
			// the winning register to everyone — including the sender, which is
			// harmless because applying it is idempotent.
			if c.in.Register != nil {
				if !c.client.acceptsNode(c.in.Register.TS.Node) {
					r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"node id mismatch"`)})
					continue
				}
				r.noteClock(c.client, c.in.Register.TS.Counter)
				winner, ok := r.applyAndPersist(c.in.Key, *c.in.Register)
				if !ok {
					r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"persistence failed"`)})
					continue
				}
				r.broadcast(Outbound{Type: msgOp, Key: c.in.Key, Register: &winner}, nil)
				r.publishCluster(ClusterEvent{Room: r.id, Kind: "op", From: r.nodeID, Epoch: r.epoch, Key: c.in.Key, Register: &winner})
			}

		case "hello":
			if !c.client.canRead() {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"read capability required"`)})
				continue
			}
			if c.in.Epoch != r.epoch {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"stale epoch"`)})
				continue
			}
			if len(c.in.State) > 0 && !c.client.canWrite() {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"write capability required"`)})
				continue
			}
			// Validate vector timestamps: client's vector must not be ahead of
			// server's clock for keys the client is NOT also sending in State.
			// For keys in State, the client is sending the actual register, so
			// an ahead vector is expected (offline edits).
			vectorOK := true
			snap := r.doc.Snapshot()
			for k, vts := range c.in.Vector {
				if _, inState := c.in.State[k]; inState {
					continue // client is sending this register, ahead vector is expected
				}
				if reg, ok := snap[k]; ok && vts.After(reg.TS) {
					vectorOK = false
					break
				}
			}
			if !vectorOK {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"vector ahead of server clock"`)})
				continue
			}
			if len(c.in.State) > 0 && !c.client.canWrite() {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"write capability required"`)})
				continue
			}
			// Validate the authoring node binding atomically, then merge only the
			// dirty registers the client sent in its hello.
			nodeOK := true
			for _, reg := range c.in.State {
				if !c.client.acceptsNode(reg.TS.Node) {
					nodeOK = false
					break
				}
			}
			if !nodeOK {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"node id mismatch"`)})
				continue
			}
			var maxCounter uint64
			persistFailed := false
			for k, reg := range c.in.State {
				if reg.TS.Counter > maxCounter {
					maxCounter = reg.TS.Counter
				}
				winner, ok := r.applyAndPersist(k, reg)
				if !ok {
					persistFailed = true
					continue
				}
				r.broadcast(Outbound{Type: msgOp, Key: k, Register: &winner}, c.client)
			}
			r.noteClock(c.client, maxCounter)
			if persistFailed {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"persistence failed"`)})
				continue
			}
			// Send only registers newer than the client's state vector. Anything
			// at or behind the client's timestamp is already in its local replica.
			delta := map[string]crdt.Register{}
			for k, reg := range r.doc.Snapshot() {
				vts, ok := c.in.Vector[k]
				if !ok || reg.TS.After(vts) {
					delta[k] = reg
				}
			}
			r.sendTo(c.client, Outbound{Type: msgDelta, State: delta, Epoch: r.epoch})
			if len(c.in.State) > 0 {
				r.publishCluster(ClusterEvent{Room: r.id, Kind: "snapshot", From: r.nodeID, Epoch: r.epoch, State: r.doc.Snapshot()})
			}

		case "merge":
			if !c.client.canWrite() {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"write capability required"`)})
				continue
			}
			if c.in.Epoch != r.epoch {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"stale epoch"`)})
				continue
			}
			// A (re)connecting client ships its full local state — this is how
			// edits made while offline propagate. Merge each register, broadcast
			// the winners to the other clients, then send the merging client a
			// fresh snapshot so it converges with everything it missed.
			//
			// Validate node binding before mutating anything so a bad merge is
			// rejected atomically rather than partially applied.
			nodeOK := true
			for _, reg := range c.in.State {
				if !c.client.acceptsNode(reg.TS.Node) {
					nodeOK = false
					break
				}
			}
			if !nodeOK {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"node id mismatch"`)})
				continue
			}
			var maxCounter uint64
			persistFailed := false
			for k, reg := range c.in.State {
				if reg.TS.Counter > maxCounter {
					maxCounter = reg.TS.Counter
				}
				winner, ok := r.applyAndPersist(k, reg)
				if !ok {
					persistFailed = true
					continue
				}
				r.broadcast(Outbound{Type: msgOp, Key: k, Register: &winner}, c.client)
			}
			r.noteClock(c.client, maxCounter)
			if persistFailed {
				// One structured failure, and no snapshot: the client must
				// retry rather than believe a partially-durable merge landed.
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"persistence failed"`)})
				continue
			}
			r.sendTo(c.client, Outbound{Type: msgSnapshot, State: r.doc.Snapshot(), Epoch: r.epoch})
			r.publishCluster(ClusterEvent{Room: r.id, Kind: "snapshot", From: r.nodeID, Epoch: r.epoch, State: r.doc.Snapshot()})

		case "ack":
			if !c.client.canRead() {
				continue
			}
			if c.in.Epoch != r.epoch {
				continue
			}
			// Idle clients advertise their clock so the GC low-water mark can
			// advance even when they aren't writing.
			r.noteClock(c.client, c.in.Clock)

		case "presence":
			if !c.client.canPresence() {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"presence capability required"`)})
				continue
			}
			if c.in.Epoch != r.epoch {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"stale epoch"`)})
				continue
			}
			r.broadcast(Outbound{Type: msgPresence, ClientID: c.client.id, Data: c.in.Data}, c.client)
			r.publishCluster(ClusterEvent{Room: r.id, Kind: "presence", From: r.nodeID, Epoch: r.epoch, ClientID: c.client.id, Data: c.in.Data})

		case "cluster":
			if c.cluster == nil || c.cluster.From == r.nodeID {
				continue
			}
			r.applyCluster(*c.cluster)

		case "export":
			c.export <- r.doc.Snapshot()

		case "stats":
			s := r.doc.Stats()
			c.reply <- RoomStats{Clients: len(r.clients), Live: s.Live, Tombstones: s.Tombstones, LowWater: r.lowWater(), Owner: r.owner}

		case "resnapshot":
			r.broadcast(Outbound{Type: msgSnapshot, State: r.doc.Snapshot(), Epoch: r.epoch}, nil)
			if r.store != nil {
				if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
					LogEvent("room_compact_error", map[string]any{"room": r.id, "error": err.Error()})
				}
			}
			LogEvent("room_resnapshot", map[string]any{"room": r.id})
			if c.result != nil {
				c.result <- true
			}

		case "force_evict":
			r.forceEvict()
			if c.result != nil {
				c.result <- true
			}
			return

		case "evict":
			if r.evictIfEmpty() {
				return
			}

		case "shutdown":
			for cl := range r.clients {
				r.removeClient(cl)
			}
			if r.unsubscribe != nil {
				r.unsubscribe()
				r.unsubscribe = nil
			}
			if c.done != nil {
				close(c.done)
			}
			return
		}
	}
}

func (r *Room) applyCluster(ev ClusterEvent) {
	switch ev.Kind {
	case "op":
		if ev.Register == nil || (ev.Epoch != 0 && ev.Epoch != r.epoch) {
			return
		}
		// Cluster writes take the same durable, clock-advancing path as local
		// writes; a remote op must survive a restart just like a local one.
		winner, ok := r.applyAndPersist(ev.Key, *ev.Register)
		if !ok {
			LogEvent("cluster_persist_error", map[string]any{"room": r.id, "key": ev.Key, "from": ev.From})
			return
		}
		r.broadcast(Outbound{Type: msgOp, Key: ev.Key, Register: &winner, Epoch: r.epoch}, nil)
	case "presence":
		clientID := ev.ClientID
		if clientID == "" {
			clientID = ev.From
		}
		r.broadcast(Outbound{Type: msgPresence, ClientID: clientID, Data: ev.Data, Epoch: r.epoch}, nil)
	case "snapshot":
		if ev.State == nil || (ev.Epoch != 0 && ev.Epoch != r.epoch) {
			return
		}
		// A snapshot is a set of registers, not an authoritative replacement:
		// join per key so concurrent local writes are preserved (CRDT merge).
		for k, reg := range ev.State {
			if _, ok := r.applyAndPersist(k, reg); !ok {
				LogEvent("cluster_persist_error", map[string]any{"room": r.id, "key": k, "from": ev.From})
			}
		}
		r.broadcast(Outbound{Type: msgSnapshot, State: r.doc.Snapshot(), Epoch: r.epoch}, nil)
	}
}

func (r *Room) publishCluster(ev ClusterEvent) {
	if r.bus == nil {
		if r.relay == nil {
			return
		}
	}
	if r.bus != nil {
		r.bus.Publish(ev)
	}
	if r.relay != nil {
		r.relay.Publish(ev)
	}
}

// broadcast sends to every client except `except`. A client whose buffer is
// full is dropped (back-pressure protection): one slow consumer must not stall
// the whole room.
func (r *Room) broadcast(o Outbound, except *Client) {
	if o.Protocol == 0 {
		o.Protocol = protocolVersion
	}
	b, err := json.Marshal(o)
	if err != nil {
		LogEvent("room_marshal_error", map[string]any{"room": r.id, "error": err.Error(), "type": o.Type})
		return
	}
	for cl := range r.clients {
		if cl == except {
			continue
		}
		r.trackSent(cl, o)
		r.deliver(cl, b)
	}
}

func (r *Room) sendTo(cl *Client, o Outbound) {
	if o.Protocol == 0 {
		o.Protocol = protocolVersion
	}
	b, err := json.Marshal(o)
	if err != nil {
		return
	}
	r.trackSent(cl, o)
	r.deliver(cl, b)
}

// trackSent records the highest logical counter the server has put on the wire
// for a client. Acknowledgements are clamped to this value, so a client cannot
// inflate the GC low-water mark beyond state the server actually delivered.
func (r *Room) trackSent(cl *Client, o Outbound) {
	var max uint64
	if o.Register != nil && o.Register.TS.Counter > max {
		max = o.Register.TS.Counter
	}
	for _, reg := range o.State {
		if reg.TS.Counter > max {
			max = reg.TS.Counter
		}
	}
	if max > r.sent[cl] {
		r.sent[cl] = max
	}
}

func (r *Room) deliver(cl *Client, b []byte) {
	if r.hub != nil && r.hub.metrics != nil {
		r.hub.metrics.outboundFrameBytes.observe(float64(len(b)))
	}
	select {
	case cl.send <- b:
	default:
		// Slow/stuck client: evict it. Safe to delete during range in Go.
		r.removeClient(cl)
	}
}

func (r *Room) removeClient(cl *Client) bool {
	if _, ok := r.clients[cl]; !ok {
		delete(r.acked, cl)
		delete(r.sent, cl)
		return false
	}
	delete(r.clients, cl)
	delete(r.acked, cl)
	delete(r.sent, cl)
	close(cl.send)
	if len(r.clients) == 0 {
		r.armIdle()
	}
	return true
}

// Config configures a Hub. The zero value is a valid, fully-open dev server.
type Config struct {
	Store                store.Store   // nil = in-memory only
	CompactEvery         int           // <=0 = default
	Secret               string        // "" = auth disabled (open) unless OIDC is set
	PreviousSecrets      []string      // old room-token secrets accepted during rotation
	OIDC                 *OIDCVerifier // external JWT verifier; nil = disabled
	AdminToken           string        // "" = operational endpoints open
	RevokedJTIs          map[string]struct{}
	AdminRatePerMinute   int           // <=0 = default 120; <0 disables limiting
	WSRatePerMinute      int           // <=0 = default 300 connection attempts/min per IP; <0 disables
	AdminBurst           int           // <=0 = default 30
	AdminMaxFailures     int           // <=0 = default 10
	AdminLockout         time.Duration // <=0 = default 5m
	AdminAllowlist       []string      // CIDRs; empty = allow any peer
	ClusterRatePerMinute int           // <=0 = default 6000; <0 disables
	ClusterAllowlist     []string      // CIDRs; empty = allow any peer
	TrustProxyHeaders    bool          // trust X-Forwarded-For from a proxy
	ProxyHops            int           // trusted proxies between us and the client (0 = 1); XFF is parsed right-to-left
	Origins              []string      // empty = allow any Origin
	MaxClients           int           // 0 = unlimited per room; main.go defaults this to 256
	MaxRooms             int           // 0 = unlimited live rooms; main.go defaults this to 10000
	MaxLogBytes          int64         // 0 = unlimited room log size before compaction
	IdleRoomTTL          time.Duration // <=0 = default, <0 disables idle eviction
	Limits               Limits        // zero fields use safe defaults
	NodeID               string        // optional local node id for placement
	Peers                []string      // optional peer node ids for placement
	Planner              *ShardPlanner
	Bus                  ClusterBus
	ClusterPeers         []string
	ClusterPath          string
	ClusterHTTP          *http.Client
	AntiEntropyInterval  time.Duration // <=0 = disabled
	WebhookURLs          []string
	WebhookSecret        string
	WebhookTimeout       time.Duration
	WebhookQueue         int
}

// Validate reports clustering configuration that cannot work. Single-node mode
// (no ClusterPeers) is always valid.
func (c Config) Validate() error {
	if c.Secret == "" && len(c.PreviousSecrets) > 0 {
		return errors.New("-secret-previous requires -secret")
	}
	if _, err := parseCIDRs(c.AdminAllowlist); err != nil {
		return fmt.Errorf("-admin-allowlist: %w", err)
	}
	if _, err := parseCIDRs(c.ClusterAllowlist); err != nil {
		return fmt.Errorf("-cluster-allowlist: %w", err)
	}
	if len(c.ClusterPeers) == 0 {
		return nil
	}
	id := strings.TrimSpace(c.NodeID)
	if id == "" || id == "local" {
		return errors.New("clustering requires -node-id set to a unique value (not the default \"local\")")
	}
	if c.Secret != "" && c.AdminToken == "" {
		return errors.New("clustering with -secret requires -admin-token so peers can authenticate")
	}
	return nil
}

// Hub multiplexes rooms by id, creating them lazily on first join.
type Hub struct {
	mu       sync.Mutex
	rooms    map[string]*Room
	cfg      Config
	planner  *ShardPlanner
	nodeID   string
	bus      ClusterBus
	relay    ClusterRelay
	idleTTL  time.Duration
	maxRooms int
	closing  atomic.Bool

	adminLimiter   *tokenLimiter
	clusterLimiter *tokenLimiter
	wsLimiter      *tokenLimiter
	adminNets      []*net.IPNet
	clusterNets    []*net.IPNet
	wsNets         []*net.IPNet

	metrics  *hubMetrics
	webhooks *WebhookDispatcher

	aeStop chan struct{}
	aeDone chan struct{}
	aeOnce sync.Once

	clusterIngested atomic.Uint64
	clusterDropped  atomic.Uint64
}

// hubMetrics holds process-wide histograms (no per-room labels to keep
// cardinality bounded).
type hubMetrics struct {
	applyDuration      *histogram
	inboundFrameBytes  *histogram
	outboundFrameBytes *histogram
}

func newHubMetrics() *hubMetrics {
	return &hubMetrics{
		applyDuration: newHistogram([]float64{
			0.0001, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1,
		}),
		inboundFrameBytes: newHistogram([]float64{
			128, 512, 1024, 4096, 16384, 65536, 262144, 1048576,
		}),
		outboundFrameBytes: newHistogram([]float64{
			128, 512, 1024, 4096, 16384, 65536, 262144, 1048576,
		}),
	}
}

// NewHub returns a hub for the given config.
func NewHub(cfg Config) *Hub {
	if cfg.CompactEvery <= 0 {
		cfg.CompactEvery = defaultCompactEvery
	}
	cfg.Limits = cfg.Limits.withDefaults()
	idleTTL := cfg.IdleRoomTTL
	if idleTTL == 0 {
		idleTTL = defaultIdleRoomTTL
	} else if idleTTL < 0 {
		idleTTL = 0 // explicitly disabled
	}
	nodeID := cfg.NodeID
	if nodeID == "" {
		nodeID = "local"
	}
	planner := cfg.Planner
	if planner == nil {
		planner = NewShardPlanner(append([]string{nodeID}, cfg.Peers...)...)
	}
	var relay ClusterRelay
	if len(cfg.ClusterPeers) > 0 {
		hr := NewHTTPClusterRelay(cfg.ClusterPeers, cfg.ClusterPath, cfg.ClusterHTTP)
		hr.Token = cfg.AdminToken
		relay = hr
	}
	adminNets, err := parseCIDRs(cfg.AdminAllowlist)
	if err != nil {
		LogEvent("invalid_admin_allowlist", map[string]any{"error": err.Error()})
	}
	clusterNets, err := parseCIDRs(cfg.ClusterAllowlist)
	if err != nil {
		LogEvent("invalid_cluster_allowlist", map[string]any{"error": err.Error()})
	}
	clusterRate := cfg.ClusterRatePerMinute
	if clusterRate == 0 {
		clusterRate = 6000
	}
	wsRate := cfg.WSRatePerMinute
	if wsRate == 0 {
		wsRate = 300
	}
	wsBurst := 60
	if wsRate > 0 && wsRate < wsBurst {
		wsBurst = wsRate
	}
	h := &Hub{
		rooms:          map[string]*Room{},
		cfg:            cfg,
		planner:        planner,
		nodeID:         nodeID,
		bus:            cfg.Bus,
		relay:          relay,
		idleTTL:        idleTTL,
		maxRooms:       cfg.MaxRooms,
		adminNets:      adminNets,
		clusterNets:    clusterNets,
		wsNets:         nil,
		adminLimiter:   newTokenLimiter(cfg.AdminRatePerMinute, cfg.AdminBurst, cfg.AdminMaxFailures, cfg.AdminLockout),
		clusterLimiter: newTokenLimiter(clusterRate, 600, cfg.AdminMaxFailures, cfg.AdminLockout),
		wsLimiter:      newTokenLimiter(wsRate, wsBurst, cfg.AdminMaxFailures, cfg.AdminLockout),
		metrics:        newHubMetrics(),
		webhooks: NewWebhookDispatcher(WebhookConfig{
			URLs:    cfg.WebhookURLs,
			Secret:  cfg.WebhookSecret,
			Timeout: cfg.WebhookTimeout,
			Queue:   cfg.WebhookQueue,
		}),
	}
	if cfg.AntiEntropyInterval > 0 && len(cfg.ClusterPeers) > 0 {
		h.aeStop = make(chan struct{})
		h.aeDone = make(chan struct{})
		go h.antiEntropyLoop(cfg.AntiEntropyInterval)
	}
	return h
}

// antiEntropyLoop periodically pushes each live room's full state to peers so
// events dropped by a saturated queue or a peer restart still converge.
func (h *Hub) antiEntropyLoop(interval time.Duration) {
	defer close(h.aeDone)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			h.antiEntropyOnce()
		case <-h.aeStop:
			return
		}
	}
}

func (h *Hub) antiEntropyOnce() {
	h.mu.Lock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	for _, r := range rooms {
		state := make(chan map[string]crdt.Register, 1)
		select {
		case r.cmds <- command{kind: "export", export: state}:
		case <-r.done:
			continue
		case <-h.aeStop:
			return
		}
		select {
		case snap := <-state:
			if len(snap) == 0 {
				continue
			}
			ev := ClusterEvent{Room: r.id, Kind: "snapshot", From: h.nodeID, State: snap}
			if h.bus != nil {
				h.bus.Publish(ev)
			}
			if h.relay != nil {
				h.relay.Publish(ev)
			}
		case <-h.aeStop:
			return
		case <-time.After(time.Second):
			LogEvent("anti_entropy_timeout", map[string]any{"room": r.id})
		}
	}
}

// room returns the room for id, creating it if necessary. It returns
// errRoomCapacity when creating a new room would exceed Config.MaxRooms.
func (h *Hub) room(id string) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[id]; ok {
		return r, nil
	}
	if h.maxRooms > 0 && len(h.rooms) >= h.maxRooms {
		return nil, errRoomCapacity
	}
	owner := ""
	if h.planner != nil {
		owner = h.planner.Owner(id)
	}
	r := newRoom(id, owner, h, h.idleTTL, h.cfg.Store, h.cfg.CompactEvery, h.cfg.MaxClients, h.cfg.MaxLogBytes, h.cfg.Limits, h.bus, h.relay, h.nodeID)
	h.rooms[id] = r
	LogEvent("room_created", map[string]any{"room": id, "owner": owner, "node": h.nodeID})
	h.emitWebhook("room.created", id, map[string]any{"owner": owner})
	return r, nil
}

// removeRoom deletes an evicted room from the live set (called by the room's
// own goroutine, so the hub map stays consistent).
func (h *Hub) removeRoom(r *Room) {
	h.mu.Lock()
	if h.rooms[r.id] == r {
		delete(h.rooms, r.id)
	}
	h.mu.Unlock()
}

// Ready reports whether the hub should accept new realtime traffic.
func (h *Hub) Ready() bool {
	return !h.closing.Load()
}

// Shutdown rejects new clients and asks all room owner goroutines to close
// their clients and exit. It is safe to call more than once.
func (h *Hub) Shutdown(ctx context.Context) error {
	h.closing.Store(true)
	if h.aeStop != nil {
		h.aeOnce.Do(func() { close(h.aeStop) })
		select {
		case <-h.aeDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	h.mu.Lock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.rooms = map[string]*Room{}
	h.mu.Unlock()

	for _, r := range rooms {
		done := make(chan struct{})
		select {
		case r.cmds <- command{kind: "shutdown", done: done}:
		case <-r.done:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}

		select {
		case <-done:
		case <-r.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if h.webhooks != nil {
		h.webhooks.Close(ctx)
	}
	return nil
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

// lookupRoom returns an existing room without creating one.
func (h *Hub) lookupRoom(id string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rooms[id]
}

// ResnapshotRoom pushes the current document to every connected client and
// compacts the persisted state. Returns false for an unknown room.
func (h *Hub) ResnapshotRoom(id string) bool {
	r := h.lookupRoom(id)
	if r == nil {
		return false
	}
	result := make(chan bool, 1)
	select {
	case r.cmds <- command{kind: "resnapshot", result: result}:
	case <-r.done:
		return false
	}
	select {
	case ok := <-result:
		return ok
	case <-r.done:
		return false
	case <-time.After(2 * time.Second):
		return false
	}
}

// EvictRoom tears down a room (closing its clients) regardless of occupancy.
func (h *Hub) EvictRoom(id string) bool {
	r := h.lookupRoom(id)
	if r == nil {
		return false
	}
	result := make(chan bool, 1)
	select {
	case r.cmds <- command{kind: "force_evict", result: result}:
	case <-r.done:
		return false
	}
	select {
	case ok := <-result:
		return ok
	case <-r.done:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
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
		select {
		case r.cmds <- command{kind: "stats", reply: reply}:
		case <-r.done:
			continue
		}
		select {
		case out[ids[i]] = <-reply:
		case <-r.done:
		}
	}
	return out
}

var clientSeq uint64

func nextClientID() string {
	return "c" + strconv.FormatUint(atomic.AddUint64(&clientSeq, 1), 10)
}
