package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
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
var errHubClosing = errors.New("server is shutting down")

// errDocCapacity is returned when a winning write would exceed the per-room
// document entry or byte budget.
var errDocCapacity = errors.New("document capacity reached")

// errPersist is returned when the store rejects a winning write.
var errPersist = errors.New("persistence failed")

// errLoadFailed is returned when persisted state could not be restored or was
// rejected by the configured limits. The room refuses joins and writes so a
// rejected log can never be overwritten by an empty compaction.
var errLoadFailed = errors.New("stored room state is unavailable")

// defaultCompactEvery is how many winning writes trigger a maintenance pass
// (tombstone GC + snapshot compaction). Configurable via NewHub.
const defaultCompactEvery = 256

// Public defaults for the documented negative-value semantics. main.go uses
// these for flag defaults; NewHub also normalizes negative configuration.
const (
	DefaultMaxClients = 256
	DefaultMaxRooms   = 10000
	DefaultMaxConns   = 4096
)

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
	id             string
	room           *Room
	send           chan []byte
	claims         TokenClaims
	node           string // authoring node id; pinned to prevent mid-stream impersonation
	delta          bool   // client requested state-vector delta sync on join
	dead           bool   // room-owned: send has been closed or the client was evicted
	maxQueueBytes  int64  // queued outbound byte budget (0 = unlimited)
	queuedBytes    atomic.Int64
	hub            *Hub
	connectionOnce sync.Once
}

func (c *Client) releaseConnection() {
	c.connectionOnce.Do(func() {
		if c.hub != nil {
			c.hub.connectionsCurrent.Add(-1)
		}
	})
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
	kind      string // "join" | "leave" | "op" | "merge" | "presence" | "ack" | "stats"
	requestID string
	client    *Client
	in        Inbound
	cluster   *ClusterEvent
	reply     chan RoomStats                // for "stats"
	export    chan map[string]crdt.Register // for "export"
	result    chan bool                     // for admin actions
	done      chan struct{}                 // for "shutdown"
	roomErr   chan error                    // for purge
	idleOnly  bool
	digest    chan string
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
	loadErr         error
	idleTTL         time.Duration
	idleTimer       *time.Timer
	redisLog        RedisRoomStore
	redisLease      RedisLease
	redisOffset     string
	redisOps        int
	redisLost       atomic.Bool
}

func (r *Room) logRoomID() string {
	if r.redisLog != nil {
		return redisRoomHash(r.id)
	}
	return r.id
}

type roomRedisLease struct {
	log   RedisRoomStore
	lease RedisLease
}

func newRoom(id, owner string, hub *Hub, idleTTL time.Duration, st store.Store, compactEvery, maxClients int, maxLogBytes int64, limits Limits, bus ClusterBus, relay ClusterRelay, nodeID string, redisLease ...roomRedisLease) *Room {
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
	if len(redisLease) > 0 {
		r.redisLog = redisLease[0].log
		r.redisLease = redisLease[0].lease
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
func (r *Room) applyAndPersist(key string, reg crdt.Register) (crdt.Register, error) {
	if r.loadErr != nil {
		return crdt.Register{}, errLoadFailed
	}
	if r.redisLog != nil && r.redisLost.Load() {
		return crdt.Register{}, errRedisFenceLost
	}
	_, span := otel.Tracer("syncwell").Start(context.Background(), "crdt.apply",
		trace.WithAttributes(
			attribute.String("syncwell.room", r.logRoomID()),
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
		return winner, nil
	}
	if winner.Equal(reg) { // the incoming write won
		if !r.doc.Fits(key, reg, r.limits.MaxDocEntries, r.limits.MaxDocBytes) {
			return cur, errDocCapacity
		}
		if r.redisLog != nil {
			id, err := r.redisLog.Append(context.Background(), r.redisLease, ClusterEvent{
				Room: r.id, Kind: "op", From: r.nodeID, Epoch: r.epoch,
				Key: key, Register: &winner,
			})
			if err != nil {
				if r.hub != nil {
					r.hub.redisStreamErrors.Add(1)
				}
				r.markRedisLost()
				return cur, errRedisFenceLost
			}
			r.redisOffset = id
			r.redisOps++
		}
		if r.store != nil {
			if err := r.store.Append(r.id, store.Record{Key: key, Reg: winner}); err != nil {
				span.SetStatus(codes.Error, "persist failed")
				LogEvent("room_persist_error", map[string]any{"room": r.logRoomID(), "error": err.Error(), "key": key})
				return cur, errPersist
			}
		}
		r.doc.ApplyRegister(key, reg)
		r.opsSinceCompact++
		if r.opsSinceCompact >= r.compactEvery || r.logFull() {
			r.maintain()
			r.opsSinceCompact = 0
		}
		return winner, nil
	}
	return winner, nil
}

func (r *Room) markRedisLost() {
	if r.redisLost.Swap(true) {
		return
	}
	if r.hub != nil {
		r.hub.redisLeaseLosses.Add(1)
	}
	select {
	case r.cmds <- command{kind: "lease-lost"}:
	default:
	}
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
	if r.loadErr != nil {
		return
	}
	// Tombstone collection is a server-memory optimisation; it must not change
	// the visible document or the epoch. Bumping the epoch here would reject
	// in-flight ops from connected clients and force a reconnect for no
	// observable benefit (clients keep their own tombstones harmlessly).
	if lw := r.lowWater(); lw > 0 {
		if n := r.doc.CollectTombstones(lw); n > 0 {
			LogEvent("room_gc", map[string]any{"room": r.logRoomID(), "tombstones": n, "low_water": lw})
		}
	}
	if r.store != nil {
		if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
			if r.hub != nil {
				r.hub.compactionFailures.Add(1)
			}
			LogEvent("room_compact_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
		} else if !r.persisted {
			r.persisted = true
			r.hub.emitWebhook("room.persisted", r.id, map[string]any{"registers": len(r.doc.Snapshot())})
		}
	}
	if r.redisLog != nil && r.redisOffset != "" {
		if err := r.saveRedisSnapshot(); err != nil {
			if r.hub != nil {
				r.hub.redisStreamErrors.Add(1)
			}
			if r.hub != nil {
				r.hub.compactionFailures.Add(1)
			}
			LogEvent("redis_snapshot_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
		}
	}
}

func (r *Room) saveRedisSnapshot() error {
	state := r.doc.Snapshot()
	encoded := make(map[string]json.RawMessage, len(state))
	for key, reg := range state {
		b, err := json.Marshal(reg)
		if err != nil {
			return err
		}
		encoded[key] = b
	}
	return r.redisLog.SaveSnapshot(context.Background(), r.redisLease, r.id, r.redisOffset, encoded)
}

func (r *Room) loadRedisState() error {
	started := time.Now()
	defer func() { r.hub.metrics.redisReplay.observe(time.Since(started).Seconds()) }()
	offset, encoded, err := r.redisLog.LoadSnapshot(context.Background(), r.id)
	if err != nil {
		r.hub.redisStreamErrors.Add(1)
		return err
	}
	r.redisOffset = offset
	for key, raw := range encoded {
		var reg crdt.Register
		if err := json.Unmarshal(raw, &reg); err != nil {
			return err
		}
		if !r.doc.Fits(key, reg, r.limits.MaxDocEntries, r.limits.MaxDocBytes) {
			return errDocCapacity
		}
		r.doc.ApplyRegister(key, reg)
		r.clock.Observe(reg.TS)
	}
	for pages := 0; pages < 1_000_000; pages++ {
		events, last, err := r.redisLog.Replay(context.Background(), r.id, offset, 1000)
		if err != nil {
			r.hub.redisStreamErrors.Add(1)
			return err
		}
		if len(events) == 0 {
			return nil
		}
		for _, ev := range events {
			if ev.Kind != "op" || ev.Register == nil {
				continue
			}
			if !r.doc.Fits(ev.Key, *ev.Register, r.limits.MaxDocEntries, r.limits.MaxDocBytes) {
				return errDocCapacity
			}
			r.doc.ApplyRegister(ev.Key, *ev.Register)
			r.clock.Observe(ev.Register.TS)
		}
		offset = last
		r.redisOffset = last
		if len(events) < 1000 {
			return nil
		}
	}
	return errors.New("redis replay page limit exceeded")
}

func (r *Room) renewRedisLease() {
	ticker := r.hub.redisTickerFactory(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.Chan():
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := r.redisLog.Renew(ctx, r.redisLease)
			cancel()
			if err == nil {
				continue
			}
			r.markRedisLost()
			return
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
// a client offline longer than a GC cycle is not counted — see docs/architecture/DESIGN.md §4.
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
	limits := store.LoadLimits{
		MaxEntries:    r.limits.MaxDocEntries,
		MaxBytes:      r.limits.MaxDocBytes,
		MaxKeyBytes:   r.limits.MaxKeyBytes,
		MaxValueBytes: r.limits.MaxValueBytes,
		MaxNodeBytes:  r.limits.MaxNodeIDBytes,
	}
	var (
		snap map[string]crdt.Register
		err  error
	)
	if loader, ok := r.store.(interface {
		LoadWithLimits(string, store.LoadLimits) (map[string]crdt.Register, error)
	}); ok {
		snap, err = loader.LoadWithLimits(r.id, limits)
	} else {
		snap, err = r.store.Load(r.id)
	}
	if err == nil {
		err = r.validateSnapshot(snap)
	}
	if err != nil {
		r.loadErr = err
		LogEvent("room_load_error", map[string]any{"room": r.logRoomID(), "error": err.Error(), "action": "refusing joins and writes; original log left intact"})
		return
	}
	r.doc.Load(snap)
	for _, reg := range snap {
		r.clock.Observe(reg.TS) // never re-issue a timestamp we've already used
	}
}

// validateSnapshot re-applies the live input policy to state read from disk.
// Stores that implement LoadWithLimits already enforce the budgets during
// replay; this covers custom stores and guards against impossible registers.
func (r *Room) validateSnapshot(snap map[string]crdt.Register) error {
	for key, reg := range snap {
		if key == "" || len(key) > r.limits.MaxKeyBytes {
			return fmt.Errorf("stored record has an invalid key")
		}
		if err := validateRegister(reg, r.limits); err != nil {
			return fmt.Errorf("stored record for key %q: %w", key, err)
		}
	}
	entries, bytes := crdt.SnapshotSize(snap)
	if r.limits.MaxDocEntries > 0 && entries > r.limits.MaxDocEntries {
		return fmt.Errorf("stored room has %d registers, limit is %d", entries, r.limits.MaxDocEntries)
	}
	if r.limits.MaxDocBytes > 0 && bytes > r.limits.MaxDocBytes {
		return fmt.Errorf("stored room uses %d bytes, limit is %d", bytes, r.limits.MaxDocBytes)
	}
	return nil
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
		if r.loadErr != nil {
			LogEvent("room_compact_refused", map[string]any{"room": r.logRoomID(), "error": r.loadErr.Error()})
		} else if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
			if r.hub != nil {
				r.hub.compactionFailures.Add(1)
			}
			LogEvent("room_compact_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
		}
		if closer, ok := r.store.(interface{ CloseRoom(string) error }); ok {
			if err := closer.CloseRoom(r.id); err != nil {
				LogEvent("room_close_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
			}
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
	LogEvent("room_evicted", map[string]any{"room": r.logRoomID(), "idle_ttl": r.idleTTL.String()})
	return true
}

// forceEvict tears the room down even if clients are connected. It is used by
// the admin dashboard for operator-initiated eviction (for example, before a
// rolling restart) and broadcasts a close to every attached client.
func (r *Room) forceEvict(requestID string) {
	for cl := range r.clients {
		r.removeClient(cl)
	}
	r.disarmIdle()
	if r.store != nil {
		fields := map[string]any{"room": r.logRoomID(), "forced": true}
		if requestID != "" {
			fields["request_id"] = requestID
		}
		if r.loadErr != nil {
			fields["error"] = r.loadErr.Error()
			LogEvent("room_compact_refused", fields)
		} else if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
			if r.hub != nil {
				r.hub.compactionFailures.Add(1)
			}
			fields["error"] = err.Error()
			LogEvent("room_compact_error", fields)
		}
		if closer, ok := r.store.(interface{ CloseRoom(string) error }); ok {
			if err := closer.CloseRoom(r.id); err != nil {
				LogEvent("room_close_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
			}
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
	fields := map[string]any{"room": r.logRoomID(), "forced": true}
	if requestID != "" {
		fields["request_id"] = requestID
	}
	LogEvent("room_evicted", fields)
}

// loop is the room's single owner goroutine. It is the *only* place that reads
// or writes r.doc, r.clock, and r.clients, which is what makes the lock-free
// design correct.
func (r *Room) loop() {
	var shutdownDone chan struct{}
	defer func() {
		if shutdownDone != nil {
			close(shutdownDone)
		}
	}()
	defer close(r.done)
	if r.redisLog != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := r.redisLog.Release(ctx, r.redisLease); err != nil && !errors.Is(err, errRedisFenceLost) {
				if r.hub != nil {
					r.hub.redisStreamErrors.Add(1)
				}
				LogEvent("redis_lease_release_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
			}
			if r.hub != nil {
				r.hub.redisLeasesCurrent.Add(-1)
			}
		}()
	}
	defer func() {
		if p := recover(); p != nil {
			LogEvent("room_panic", map[string]any{"room": r.logRoomID(), "panic": fmt.Sprint(p), "stack": string(debug.Stack())})
			for cl := range r.clients {
				r.removeClient(cl)
			}
			if r.unsubscribe != nil {
				r.unsubscribe()
				r.unsubscribe = nil
			}
			if r.hub != nil {
				r.hub.removeRoom(r)
			}
		}
	}()
	if r.redisLog != nil {
		if err := r.loadRedisState(); err != nil {
			r.loadErr = err
			LogEvent("redis_replay_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
		}
	} else {
		r.loadPersisted()
	}
	for c := range r.cmds {
		if r.redisLost.Load() && c.kind != "lease-lost" && c.kind != "shutdown" {
			if c.client != nil {
				r.rejectClient(c.client, "room owner lease lost")
				r.removeClient(c.client)
			}
			continue
		}
		// Commands from clients that were rejected or evicted can still be
		// queued behind the event that removed them; drop them instead of
		// writing to a closed send channel.
		if c.client != nil && c.kind != "join" && !r.clients[c.client] {
			continue
		}
		switch c.kind {
		case "lease-lost":
			for cl := range r.clients {
				r.sendTo(cl, Outbound{Type: msgError, Data: json.RawMessage(`"room owner lease lost"`)})
				r.removeClient(cl)
			}
			if r.hub != nil {
				r.hub.removeRoom(r)
			}
			return
		case "join":
			r.disarmIdle()
			if r.loadErr != nil {
				r.rejectClient(c.client, "room state unavailable")
				continue
			}
			if r.maxClients > 0 && len(r.clients) >= r.maxClients {
				// Reject over-capacity: tell the client, then close its channel.
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"room full"`)})
				c.client.dead = true
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
				winner, err := r.applyAndPersist(c.in.Key, *c.in.Register)
				if err != nil {
					r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"` + err.Error() + `"`)})
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
			var applyFailed error
			for k, reg := range c.in.State {
				if reg.TS.Counter > maxCounter {
					maxCounter = reg.TS.Counter
				}
				winner, err := r.applyAndPersist(k, reg)
				if err != nil {
					applyFailed = err
					continue
				}
				r.broadcast(Outbound{Type: msgOp, Key: k, Register: &winner}, c.client)
			}
			r.noteClock(c.client, maxCounter)
			if applyFailed != nil {
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"` + applyFailed.Error() + `"`)})
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
			var applyFailed error
			for k, reg := range c.in.State {
				if reg.TS.Counter > maxCounter {
					maxCounter = reg.TS.Counter
				}
				winner, err := r.applyAndPersist(k, reg)
				if err != nil {
					applyFailed = err
					continue
				}
				r.broadcast(Outbound{Type: msgOp, Key: k, Register: &winner}, c.client)
			}
			r.noteClock(c.client, maxCounter)
			if applyFailed != nil {
				// One structured failure, and no snapshot: the client must
				// retry rather than believe a partially-durable merge landed.
				r.sendTo(c.client, Outbound{Type: msgError, Data: json.RawMessage(`"` + applyFailed.Error() + `"`)})
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
			if r.loadErr != nil {
				c.export <- nil
				continue
			}
			c.export <- r.doc.Snapshot()

		case "digest":
			if c.digest != nil {
				if r.loadErr != nil {
					c.digest <- ""
				} else {
					c.digest <- DigestRegisters(r.doc.Snapshot())
				}
			}

		case "stats":
			s := r.doc.Stats()
			c.reply <- RoomStats{Clients: len(r.clients), Live: s.Live, Tombstones: s.Tombstones, LowWater: r.lowWater(), Owner: r.owner}

		case "resnapshot":
			if r.loadErr != nil {
				fields := map[string]any{"room": r.logRoomID(), "error": r.loadErr.Error()}
				if c.requestID != "" {
					fields["request_id"] = c.requestID
				}
				LogEvent("room_resnapshot_refused", fields)
				if c.result != nil {
					c.result <- false
				}
				continue
			}
			r.broadcast(Outbound{Type: msgSnapshot, State: r.doc.Snapshot(), Epoch: r.epoch}, nil)
			if r.store != nil {
				if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
					if r.hub != nil {
						r.hub.compactionFailures.Add(1)
					}
					fields := map[string]any{"room": r.logRoomID(), "error": err.Error()}
					if c.requestID != "" {
						fields["request_id"] = c.requestID
					}
					LogEvent("room_compact_error", fields)
				}
			}
			fields := map[string]any{"room": r.logRoomID()}
			if c.requestID != "" {
				fields["request_id"] = c.requestID
			}
			LogEvent("room_resnapshot", fields)
			if c.result != nil {
				c.result <- true
			}

		case "force_evict":
			r.forceEvict(c.requestID)
			if c.result != nil {
				c.result <- true
			}
			return

		case "purge":
			if c.idleOnly && len(r.clients) > 0 {
				if c.roomErr != nil {
					c.roomErr <- errRoomHasClients
				}
				continue
			}
			for cl := range r.clients {
				r.removeClient(cl)
			}
			var err error
			if r.store != nil {
				err = r.store.Delete(r.id)
			}
			if err == nil && r.redisLog != nil {
				err = r.redisLog.Purge(context.Background(), r.redisLease, r.id)
			}
			if err != nil {
				if c.roomErr != nil {
					c.roomErr <- err
				}
				continue
			}
			LogEvent("room_purged", map[string]any{"room": r.logRoomID(), "request_id": c.requestID})
			r.hub.emitWebhook("room.purged", r.id, map[string]any{"request_id": c.requestID})
			if r.hub != nil {
				r.hub.removeRoom(r)
			}
			if c.roomErr != nil {
				c.roomErr <- nil
			}
			return

		case "evict":
			if r.evictIfEmpty() {
				return
			}

		case "shutdown":
			if r.store != nil {
				if err := r.store.Compact(r.id, r.doc.Snapshot()); err != nil {
					LogEvent("shutdown_room_flush_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
				}
			}
			if r.redisLog != nil && r.redisOffset != "" {
				if err := r.saveRedisSnapshot(); err != nil {
					r.hub.redisStreamErrors.Add(1)
					LogEvent("shutdown_redis_snapshot_error", map[string]any{"room": r.logRoomID(), "error": err.Error()})
				}
			}
			for cl := range r.clients {
				r.removeClient(cl)
			}
			if r.unsubscribe != nil {
				r.unsubscribe()
				r.unsubscribe = nil
			}
			if c.done != nil {
				shutdownDone = c.done
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
		winner, err := r.applyAndPersist(ev.Key, *ev.Register)
		if err != nil {
			LogEvent("cluster_persist_error", map[string]any{"room": r.logRoomID(), "key": ev.Key, "from": ev.From, "error": err.Error()})
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
			if _, err := r.applyAndPersist(k, reg); err != nil {
				LogEvent("cluster_persist_error", map[string]any{"room": r.logRoomID(), "key": k, "from": ev.From, "error": err.Error()})
			}
		}
		r.broadcast(Outbound{Type: msgSnapshot, State: r.doc.Snapshot(), Epoch: r.epoch}, nil)
	case "delta":
		if ev.State == nil || (ev.Epoch != 0 && ev.Epoch != r.epoch) {
			return
		}
		for k, reg := range ev.State {
			if _, err := r.applyAndPersist(k, reg); err != nil {
				LogEvent("cluster_persist_error", map[string]any{"room": r.logRoomID(), "key": k, "from": ev.From, "error": err.Error()})
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
		LogEvent("room_marshal_error", map[string]any{"room": r.logRoomID(), "error": err.Error(), "type": o.Type})
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

// rejectClient sends one terminal error and closes the client's queue. Used
// for admission failures that happen after the websocket upgrade.
func (r *Room) rejectClient(cl *Client, reason string) {
	if cl == nil || cl.dead {
		return
	}
	data, _ := json.Marshal(reason)
	r.sendTo(cl, Outbound{Type: msgError, Data: data})
	cl.dead = true
	close(cl.send)
}

func (r *Room) sendTo(cl *Client, o Outbound) {
	if cl == nil || cl.dead {
		return
	}
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
	if cl == nil || cl.dead {
		return
	}
	if r.hub != nil && r.hub.metrics != nil {
		r.hub.metrics.outboundFrameBytes.observe(float64(len(b)))
	}
	n := int64(len(b))
	if cl.maxQueueBytes > 0 {
		queued := cl.queuedBytes.Load()
		// Permit one oversized frame when nothing is queued (a full snapshot
		// may legitimately exceed the steady-state budget), but never let the
		// queue grow past the budget once it holds data.
		if queued > 0 && queued+n > cl.maxQueueBytes {
			r.removeClient(cl)
			return
		}
	}
	select {
	case cl.send <- b:
		cl.queuedBytes.Add(n)
	default:
		// Slow/stuck client: evict it. Safe to delete during range in Go.
		r.removeClient(cl)
	}
}

func (r *Room) removeClient(cl *Client) bool {
	if _, ok := r.clients[cl]; !ok {
		delete(r.acked, cl)
		delete(r.sent, cl)
		cl.dead = true
		return false
	}
	delete(r.clients, cl)
	delete(r.acked, cl)
	delete(r.sent, cl)
	cl.dead = true
	close(cl.send)
	if len(r.clients) == 0 {
		r.armIdle()
	}
	return true
}

// Config configures a Hub. The zero value is a valid, fully-open dev server.
type Config struct {
	ClusterMode          string        // relay (default) or redis
	Store                store.Store   // nil = in-memory only
	AuditLogger          *AuditLogger  // optional append-only audit file sink; events always go to stderr
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
	Origins              []string      // exact allowed Origins (scheme://host[:port])
	RequireOrigins       bool          // reject browser upgrades without a configured, matching Origin
	AllowAnyOrigin       bool          // explicit escape hatch for non-browser clients
	MaxClients           int           // 0 = unlimited per room; main.go defaults this to 256
	MaxConns             int64         // 0 = unlimited process-wide WebSocket connections; main.go defaults this to 4096
	MaxRooms             int           // 0 = unlimited live rooms; main.go defaults this to 10000
	MaxLogBytes          int64         // 0 = unlimited room log size before compaction
	IdleRoomTTL          time.Duration // <=0 = default, <0 disables idle eviction
	Limits               Limits        // zero fields use safe defaults
	NodeID               string        // optional local node id for placement
	Peers                []string      // optional peer node ids for placement
	Planner              *ShardPlanner
	Bus                  ClusterBus
	ClusterPeers         []string
	ClusterAllowHTTP     bool // explicitly allow plaintext cluster relay (private network only)
	ClusterPath          string
	ClusterHTTP          *http.Client
	RedisRoomLog         RedisRoomStore
	NodeAdvertise        map[string]string // node id -> public wss URL for /route
	AntiEntropyInterval  time.Duration     // <=0 = disabled
	WebhookURLs          []string
	WebhookSecret        string
	WebhookTimeout       time.Duration
	WebhookQueue         int
	WebhookAllowPrivate  bool // explicitly allow private/loopback webhook destinations
	Retention            time.Duration
	RetentionDryRun      bool
	redisTickerFactory   func(time.Duration) redisTicker
}

type redisTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type wallClockRedisTicker struct{ ticker *time.Ticker }

func (t wallClockRedisTicker) Chan() <-chan time.Time { return t.ticker.C }
func (t wallClockRedisTicker) Stop()                  { t.ticker.Stop() }

// Validate reports clustering configuration that cannot work. Single-node mode
// (no ClusterPeers) is always valid.
func (c Config) Validate() error {
	mode := c.ClusterMode
	if mode == "" {
		mode = "relay"
	}
	if mode != "relay" && mode != "redis" {
		return errors.New("-cluster-mode must be relay or redis")
	}
	if mode == "redis" {
		if c.RedisRoomLog == nil {
			return errors.New("-cluster-mode=redis requires a Redis room log")
		}
		if !validRedisNodeID(strings.TrimSpace(c.NodeID)) {
			return errors.New("-cluster-mode=redis requires a node ID of 1-128 ASCII letters, digits, dot, underscore, or hyphen")
		}
		if c.NodeAdvertise[strings.TrimSpace(c.NodeID)] == "" {
			return errors.New("-cluster-mode=redis requires this node's advertised WebSocket URL")
		}
		if len(c.ClusterPeers) > 0 || len(c.Peers) > 0 {
			return errors.New("-cluster-peers and -node-peers apply only to relay mode; advertise every Redis node with -node-advertise")
		}
		if c.Retention > 0 {
			return errors.New("-retention is not supported in Redis ownership mode; use explicit room purge")
		}
	}
	if c.Secret == "" && len(c.PreviousSecrets) > 0 {
		return errors.New("-secret-previous requires -secret (or SYNCWELL_SECRET_PREVIOUS requires SYNCWELL_SECRET)")
	}
	if _, err := parseCIDRs(c.AdminAllowlist); err != nil {
		return fmt.Errorf("-admin-allowlist (SYNCWELL_ADMIN_ALLOWLIST): %w", err)
	}
	if _, err := parseCIDRs(c.ClusterAllowlist); err != nil {
		return fmt.Errorf("-cluster-allowlist (SYNCWELL_CLUSTER_ALLOWLIST): %w", err)
	}
	if c.AllowAnyOrigin && len(c.Origins) > 0 {
		return errors.New("-allow-any-origin cannot be combined with -origins (SYNCWELL_ALLOW_ANY_ORIGIN/SYNCWELL_ORIGINS)")
	}
	if c.RequireOrigins && len(c.Origins) == 0 && !c.AllowAnyOrigin {
		return errors.New("-require-origins requires at least one -origins entry (SYNCWELL_REQUIRE_ORIGINS/SYNCWELL_ORIGINS)")
	}
	for _, origin := range c.Origins {
		if _, err := normalizeOrigin(origin); err != nil {
			return fmt.Errorf("-origins entry %q: %w", origin, err)
		}
	}
	if err := ValidateWebhookURLs(c.WebhookURLs, c.WebhookAllowPrivate); err != nil {
		return fmt.Errorf("-webhook-urls (SYNCWELL_WEBHOOK_URLS): %w", err)
	}
	if mode == "redis" {
		return nil
	}
	if len(c.ClusterPeers) == 0 {
		return nil
	}
	id := strings.TrimSpace(c.NodeID)
	if id == "" || id == "local" {
		return errors.New("clustering requires -node-id set to a unique value (SYNCWELL_NODE_ID; not the default \"local\")")
	}
	if c.AdminToken == "" {
		return errors.New("clustering requires -admin-token (SYNCWELL_ADMIN_TOKEN) so peers can authenticate")
	}
	for _, raw := range c.ClusterPeers {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("-cluster-peers entry %q: %w", raw, err)
		}
		scheme := strings.ToLower(u.Scheme)
		if !u.IsAbs() || u.Opaque != "" || (scheme != "https" && scheme != "http") {
			return fmt.Errorf("-cluster-peers entry %q must use https", raw)
		}
		if u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return fmt.Errorf("-cluster-peers entry %q must be a URL with no credentials, path, query, or fragment", raw)
		}
		if scheme == "http" && !c.ClusterAllowHTTP {
			return fmt.Errorf("-cluster-peers entry %q uses plaintext http; use https or set -cluster-insecure for a trusted private network", raw)
		}
	}
	return nil
}

// Hub multiplexes rooms by id, creating them lazily on first join.
type Hub struct {
	mu       sync.Mutex
	rooms    map[string]*Room
	creating map[string]chan struct{}
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
	purging        map[string]bool

	metrics  *hubMetrics
	webhooks *WebhookDispatcher

	aeStop chan struct{}
	aeDone chan struct{}
	aeOnce sync.Once

	clusterIngested     atomic.Uint64
	clusterDropped      atomic.Uint64
	processNonce        string
	redisTickerFactory  func(time.Duration) redisTicker
	redisLeasesCurrent  atomic.Int64
	redisLeaseAcquired  atomic.Uint64
	redisRehomes        atomic.Uint64
	redisLeaseLosses    atomic.Uint64
	redisStreamErrors   atomic.Uint64
	tokenInQuery        atomic.Uint64
	lastTokenQueryLog   atomic.Int64
	connectionsCurrent  atomic.Int64
	connectionRejects   atomic.Uint64
	commandQueueRejects atomic.Uint64
	lastCommandQueueLog atomic.Int64
	compactionFailures  atomic.Uint64
	antiEntropyBytes    atomic.Uint64
	auditAuthMu         sync.Mutex
	auditAuthLast       map[string]time.Time
}

func (h *Hub) logRoomID(room string) string {
	if h.cfg.RedisRoomLog != nil {
		return redisRoomHash(room)
	}
	return room
}

// hubMetrics holds process-wide histograms (no per-room labels to keep
// cardinality bounded).
type hubMetrics struct {
	applyDuration      *histogram
	inboundFrameBytes  *histogram
	outboundFrameBytes *histogram
	redisReplay        *histogram
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
		redisReplay: newHistogram([]float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 15, 30}),
	}
}

// NewHub returns a hub for the given config.
func NewHub(cfg Config) *Hub {
	if cfg.MaxConns < 0 {
		cfg.MaxConns = DefaultMaxConns
	}
	if cfg.MaxClients < 0 {
		cfg.MaxClients = DefaultMaxClients
	}
	if cfg.MaxRooms < 0 {
		cfg.MaxRooms = DefaultMaxRooms
	}
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
	nodeID := strings.TrimSpace(cfg.NodeID)
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
		rooms:              map[string]*Room{},
		creating:           map[string]chan struct{}{},
		cfg:                cfg,
		planner:            planner,
		nodeID:             nodeID,
		bus:                cfg.Bus,
		relay:              relay,
		idleTTL:            idleTTL,
		maxRooms:           cfg.MaxRooms,
		purging:            map[string]bool{},
		adminNets:          adminNets,
		clusterNets:        clusterNets,
		wsNets:             nil,
		adminLimiter:       newTokenLimiter(cfg.AdminRatePerMinute, cfg.AdminBurst, cfg.AdminMaxFailures, cfg.AdminLockout),
		clusterLimiter:     newTokenLimiter(clusterRate, 600, cfg.AdminMaxFailures, cfg.AdminLockout),
		wsLimiter:          newTokenLimiter(wsRate, wsBurst, cfg.AdminMaxFailures, cfg.AdminLockout),
		metrics:            newHubMetrics(),
		processNonce:       newProcessNonce(),
		redisTickerFactory: cfg.redisTickerFactory,
		webhooks: NewWebhookDispatcher(WebhookConfig{
			URLs:           cfg.WebhookURLs,
			Secret:         cfg.WebhookSecret,
			Timeout:        cfg.WebhookTimeout,
			Queue:          cfg.WebhookQueue,
			AllowPrivate:   cfg.WebhookAllowPrivate,
			HashRoomInLogs: cfg.RedisRoomLog != nil,
		}),
	}
	if h.redisTickerFactory == nil {
		h.redisTickerFactory = func(interval time.Duration) redisTicker {
			return wallClockRedisTicker{ticker: time.NewTicker(interval)}
		}
	}
	if cfg.AntiEntropyInterval > 0 && len(cfg.ClusterPeers) > 0 {
		h.aeStop = make(chan struct{})
		h.aeDone = make(chan struct{})
		go h.antiEntropyLoop(cfg.AntiEntropyInterval)
	}
	return h
}

// antiEntropyLoop periodically reconciles each live room with peers so events
// dropped by a saturated queue or a peer restart still converge.
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
			if payload, err := json.Marshal(ev); err == nil && h.relay == nil {
				h.antiEntropyBytes.Add(uint64(len(payload)))
			}
			if h.bus != nil {
				h.bus.Publish(ev)
			}
			if h.relay != nil {
				if digestRelay, ok := h.relay.(interface {
					AntiEntropy(ClusterEvent, string, func(ClusterEvent)) uint64
				}); ok {
					h.antiEntropyBytes.Add(digestRelay.AntiEntropy(ev, DigestRegisters(snap), func(delta ClusterEvent) { _ = h.ingestClusterEvent(delta) }))
				} else {
					h.relay.Publish(ev)
				}
			}
		case <-h.aeStop:
			return
		case <-time.After(time.Second):
			LogEvent("anti_entropy_timeout", map[string]any{"room": h.logRoomID(r.id)})
		}
	}
}

// room returns the room for id, creating it if necessary. It returns
// errRoomCapacity when creating a new room would exceed Config.MaxRooms.
func (h *Hub) room(id string) (*Room, error) {
	for {
		h.mu.Lock()
		if h.purging[id] {
			h.mu.Unlock()
			return nil, errRoomPurging
		}
		if r, ok := h.rooms[id]; ok {
			h.mu.Unlock()
			if r.redisLost.Load() {
				return nil, errRedisFenceLost
			}
			return r, nil
		}
		if wait, ok := h.creating[id]; ok {
			h.mu.Unlock()
			<-wait
			continue
		}
		if h.closing.Load() {
			h.mu.Unlock()
			return nil, errHubClosing
		}
		if h.maxRooms > 0 && len(h.rooms) >= h.maxRooms {
			h.mu.Unlock()
			return nil, errRoomCapacity
		}
		wait := make(chan struct{})
		h.creating[id] = wait
		h.mu.Unlock()
		break
	}
	finishCreation := func() {
		h.mu.Lock()
		if wait, ok := h.creating[id]; ok {
			delete(h.creating, id)
			close(wait)
		}
		h.mu.Unlock()
	}
	owner := ""
	var lease RedisLease
	if h.cfg.RedisRoomLog != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var err error
		lease, err = h.cfg.RedisRoomLog.Acquire(ctx, id, h.nodeID, h.processNonce)
		cancel()
		if err != nil {
			finishCreation()
			return nil, err
		}
		h.redisLeasesCurrent.Add(1)
		h.redisLeaseAcquired.Add(1)
		if lease.epoch > 1 {
			h.redisRehomes.Add(1)
		}
		owner = h.nodeID
	}
	if h.planner != nil {
		if h.cfg.RedisRoomLog == nil {
			owner = h.planner.Owner(id)
		}
	}
	var redisLeaseConfig []roomRedisLease
	if h.cfg.RedisRoomLog != nil {
		redisLeaseConfig = append(redisLeaseConfig, roomRedisLease{log: h.cfg.RedisRoomLog, lease: lease})
	}
	r := newRoom(id, owner, h, h.idleTTL, h.cfg.Store, h.cfg.CompactEvery, h.cfg.MaxClients, h.cfg.MaxLogBytes, h.cfg.Limits, h.bus, h.relay, h.nodeID, redisLeaseConfig...)
	if h.cfg.RedisRoomLog != nil {
		go r.renewRedisLease()
	}
	h.mu.Lock()
	if h.closing.Load() || h.purging[id] {
		abortErr := errHubClosing
		if h.purging[id] {
			abortErr = errRoomPurging
		}
		if wait, ok := h.creating[id]; ok {
			delete(h.creating, id)
			close(wait)
		}
		h.mu.Unlock()
		done := make(chan struct{})
		select {
		case r.cmds <- command{kind: "shutdown", done: done}:
		case <-r.done:
			return nil, abortErr
		}
		select {
		case <-done:
		case <-r.done:
		}
		return nil, abortErr
	}
	h.rooms[id] = r
	if wait, ok := h.creating[id]; ok {
		delete(h.creating, id)
		close(wait)
	}
	h.mu.Unlock()
	LogEvent("room_created", map[string]any{"room": h.logRoomID(id), "owner": owner, "node": h.nodeID})
	h.emitWebhook("room.created", id, map[string]any{"owner": owner})
	return r, nil
}

func newProcessNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

var errRoomPurging = errors.New("room purge in progress")

// PurgeRoom disconnects clients, removes persisted state, and evicts the room.
func (h *Hub) PurgeRoom(id, requestID string) error {
	h.mu.Lock()
	if h.purging[id] {
		h.mu.Unlock()
		return errRoomPurging
	}
	h.purging[id] = true
	r := h.rooms[id]
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.purging, id); h.mu.Unlock() }()
	if r == nil {
		if h.cfg.Store == nil && h.cfg.RedisRoomLog == nil {
			return errors.New("room not found")
		}
		if h.cfg.Store != nil {
			if err := h.cfg.Store.Delete(id); err != nil {
				return err
			}
		}
		if h.cfg.RedisRoomLog != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			lease, err := h.cfg.RedisRoomLog.Acquire(ctx, id, h.nodeID, h.processNonce)
			if err == nil {
				err = h.cfg.RedisRoomLog.Purge(ctx, lease, id)
			}
			cancel()
			if err != nil {
				return err
			}
		}
		LogEvent("room_purged", map[string]any{"room": h.logRoomID(id), "request_id": requestID})
		h.emitWebhook("room.purged", id, map[string]any{"request_id": requestID})
		return nil
	}
	ch := make(chan error, 1)
	select {
	case r.cmds <- command{kind: "purge", requestID: requestID, roomErr: ch}:
	case <-r.done:
		return errors.New("room not found")
	}
	select {
	case err := <-ch:
		return err
	case <-r.done:
		return nil
	case <-time.After(5 * time.Second):
		return context.DeadlineExceeded
	}
}

var errRoomHasClients = errors.New("room has connected clients")

// SweepRetentionAt deletes rooms only after both their metadata creation time
// and log modification time fall outside the retention window.
func (h *Hub) SweepRetentionAt(now time.Time) error {
	if h.cfg.Retention <= 0 || h.cfg.Store == nil {
		return nil
	}
	lister, ok := h.cfg.Store.(interface {
		RoomMetadata() ([]store.RoomMetadata, error)
	})
	if !ok {
		return errors.New("configured store does not support retention metadata")
	}
	items, err := lister.RoomMetadata()
	if err != nil {
		return err
	}
	cutoff := now.Add(-h.cfg.Retention)
	for _, item := range items {
		if item.Created.After(cutoff) || item.LogModTime.After(cutoff) {
			continue
		}
		if h.cfg.RetentionDryRun {
			LogEvent("retention_would_purge", map[string]any{"room": h.logRoomID(item.Room)})
			continue
		}
		if err := h.purgeIdle(item.Room); err != nil && !errors.Is(err, errRoomHasClients) {
			LogEvent("retention_purge_error", map[string]any{"room": h.logRoomID(item.Room), "error": err.Error()})
		}
	}
	return nil
}

func (h *Hub) purgeIdle(id string) error {
	h.mu.Lock()
	if h.purging[id] {
		h.mu.Unlock()
		return nil
	}
	h.purging[id] = true
	r := h.rooms[id]
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.purging, id); h.mu.Unlock() }()
	if r == nil {
		if err := h.cfg.Store.Delete(id); err != nil {
			return err
		}
		LogEvent("room_purged", map[string]any{"room": h.logRoomID(id), "reason": "retention"})
		h.emitWebhook("room.purged", id, map[string]any{"reason": "retention"})
		return nil
	}
	ch := make(chan error, 1)
	select {
	case r.cmds <- command{kind: "purge", idleOnly: true, roomErr: ch}:
	case <-r.done:
		return nil
	}
	select {
	case err := <-ch:
		return err
	case <-r.done:
		return nil
	case <-time.After(2 * time.Second):
		return context.DeadlineExceeded
	}
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

func (h *Hub) recordCommandQueueFull(room string) {
	h.commandQueueRejects.Add(1)
	now := time.Now().UnixNano()
	last := h.lastCommandQueueLog.Load()
	if now-last >= int64(10*time.Second) && h.lastCommandQueueLog.CompareAndSwap(last, now) {
		LogEvent("room_command_queue_full", map[string]any{"room": h.logRoomID(room)})
	}
}

func (h *Hub) acquireConnection() bool {
	for {
		current := h.connectionsCurrent.Load()
		if h.cfg.MaxConns > 0 && current >= h.cfg.MaxConns {
			h.connectionRejects.Add(1)
			return false
		}
		if h.connectionsCurrent.CompareAndSwap(current, current+1) {
			return true
		}
	}
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

	completed := 0
	for _, r := range rooms {
		done := make(chan struct{})
		select {
		case r.cmds <- command{kind: "shutdown", done: done}:
		case <-r.done:
			completed++
			continue
		case <-ctx.Done():
			LogEvent("shutdown_incomplete", map[string]any{"rooms_total": len(rooms), "rooms_completed": completed, "rooms_remaining": len(rooms) - completed})
			return ctx.Err()
		}
		select {
		case <-done:
		case <-r.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		completed++
	}
	if h.webhooks != nil {
		h.webhooks.Close(ctx)
	}
	return nil
}

// normalizeOrigin accepts only a bare HTTP(S) origin and returns a canonical
// scheme://host[:port] form. Wildcards, paths, credentials, and opaque values
// are rejected: the origin policy compares exact origins, never substrings.
func normalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" || strings.Contains(raw, "*") {
		return "", errors.New("origin must be an exact scheme://host[:port] value")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse origin: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", errors.New("origin must use http or https")
	}
	if u.Host == "" || u.User != nil || u.Opaque != "" || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("origin must be scheme://host[:port] with no path, query, fragment, or credentials")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	// Browsers omit default ports from the Origin header, so canonicalize them
	// away rather than making an operator's explicit :443 never match.
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return scheme + "://" + host, nil
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// originAllowed enforces the exact origin allow-list. Missing Origin headers
// are rejected when an allow-list is configured (RequireOrigins or origins
// present); AllowAnyOrigin is an explicit, documented escape hatch.
func (h *Hub) originAllowed(origin string) bool {
	if h.cfg.AllowAnyOrigin {
		return true
	}
	if len(h.cfg.Origins) == 0 {
		return !h.cfg.RequireOrigins
	}
	if origin == "" {
		return false
	}
	got, err := normalizeOrigin(origin)
	if err != nil {
		return false
	}
	for _, allowed := range h.cfg.Origins {
		want, err := normalizeOrigin(allowed)
		if err == nil && want == got {
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
	return h.ResnapshotRoomWithRequestID(id, "")
}

func (h *Hub) ResnapshotRoomWithRequestID(id, requestID string) bool {
	r := h.lookupRoom(id)
	if r == nil {
		return false
	}
	result := make(chan bool, 1)
	select {
	case r.cmds <- command{kind: "resnapshot", result: result, requestID: requestID}:
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
	return h.EvictRoomWithRequestID(id, "")
}

func (h *Hub) EvictRoomWithRequestID(id, requestID string) bool {
	r := h.lookupRoom(id)
	if r == nil {
		return false
	}
	result := make(chan bool, 1)
	select {
	case r.cmds <- command{kind: "force_evict", result: result, requestID: requestID}:
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
// memory. The query is bounded so one wedged room cannot hang an admin request.
func (h *Hub) Stats() map[string]RoomStats {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, _ := h.StatsContext(ctx)
	return out
}

// StatsContext is Stats with a caller-supplied deadline. It returns ok=false if
// the deadline expired before every room replied; the returned map is a
// best-effort partial snapshot.
func (h *Hub) StatsContext(ctx context.Context) (map[string]RoomStats, bool) {
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
		case <-ctx.Done():
			return out, false
		}
		select {
		case out[ids[i]] = <-reply:
		case <-r.done:
		case <-ctx.Done():
			return out, false
		}
	}
	return out, true
}

var clientSeq uint64

func nextClientID() string {
	return "c" + strconv.FormatUint(atomic.AddUint64(&clientSeq, 1), 10)
}
