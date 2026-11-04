package server

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"syncwell/internal/crdt"
	"syncwell/internal/store"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// OriginPolicy is enforced explicitly before Upgrade; gorilla's own check is
	// disabled so the policy can also reject missing Origin headers.
	CheckOrigin:      func(*http.Request) bool { return true },
	HandshakeTimeout: 10 * time.Second,
}

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10

	maxMsg          = 1 << 20 // 1 MiB read cap per message
	maxRoomIDBytes  = 256
	maxKeyBytes     = 512
	maxNodeIDBytes  = 256
	maxValueBytes   = 256 * 1024
	maxPresenceSize = 64 * 1024
	maxStateEntries = 10000
	maxMessagesRate = 120
	maxDocEntries   = 100000
	maxDocBytes     = 16 << 20 // 16 MiB approximate in-memory document cap
	maxSendBytes    = 4 << 20  // 4 MiB queued outbound bytes per client
)

// Limits bounds untrusted client input. Any zero field is replaced with the
// package default by NewHub.
type Limits struct {
	MaxMessageBytes      int
	MaxRoomIDBytes       int
	MaxKeyBytes          int
	MaxNodeIDBytes       int
	MaxValueBytes        int
	MaxPresenceBytes     int
	MaxStateEntries      int
	MaxMessagesPerSecond int
	MaxDocEntries        int   // total registers (incl. tombstones) per room
	MaxDocBytes          int64 // approximate in-memory bytes per room
	MaxSendBytes         int64 // queued outbound bytes per client
}

func (l Limits) withDefaults() Limits {
	if l.MaxMessageBytes <= 0 {
		l.MaxMessageBytes = maxMsg
	}
	if l.MaxRoomIDBytes <= 0 {
		l.MaxRoomIDBytes = maxRoomIDBytes
	}
	if l.MaxKeyBytes <= 0 {
		l.MaxKeyBytes = maxKeyBytes
	}
	if l.MaxNodeIDBytes <= 0 {
		l.MaxNodeIDBytes = maxNodeIDBytes
	}
	if l.MaxValueBytes <= 0 {
		l.MaxValueBytes = maxValueBytes
	}
	if l.MaxPresenceBytes <= 0 {
		l.MaxPresenceBytes = maxPresenceSize
	}
	if l.MaxStateEntries <= 0 {
		l.MaxStateEntries = maxStateEntries
	}
	if l.MaxMessagesPerSecond <= 0 {
		l.MaxMessagesPerSecond = maxMessagesRate
	}
	if l.MaxDocEntries <= 0 {
		l.MaxDocEntries = maxDocEntries
	}
	if l.MaxDocBytes <= 0 {
		l.MaxDocBytes = maxDocBytes
	}
	if l.MaxSendBytes <= 0 {
		l.MaxSendBytes = maxSendBytes
	}
	return l
}

// ServeStats writes per-room statistics as JSON (live keys, tombstones, clients,
// GC low-water mark).
func (h *Hub) ServeStats(w http.ResponseWriter, req *http.Request) {
	if !h.adminAllowed(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	stats, ok := h.StatsContext(ctx)
	if !ok {
		http.Error(w, "stats timeout", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

// ServeMetrics writes a tiny Prometheus-style text snapshot of the current room
// set. It reuses the same admin-token protection as /stats.
func (h *Hub) ServeMetrics(w http.ResponseWriter, req *http.Request) {
	if !h.adminAllowed(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	stats, ok := h.StatsContext(ctx)
	if !ok {
		http.Error(w, "stats timeout", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintln(w, "# HELP syncwell_rooms Number of open rooms")
	fmt.Fprintln(w, "# TYPE syncwell_rooms gauge")
	fmt.Fprintf(w, "syncwell_rooms %d\n", len(stats))
	fmt.Fprintln(w, "# HELP syncwell_clients_total Total connected clients across rooms")
	fmt.Fprintln(w, "# TYPE syncwell_clients_total gauge")
	var clients, live, tombstones, lowWaterStuck uint64
	for _, s := range stats {
		clients += uint64(s.Clients)
		live += uint64(s.Live)
		tombstones += uint64(s.Tombstones)
		if s.Clients > 0 && s.LowWater == 0 {
			lowWaterStuck++
		}
	}
	fmt.Fprintf(w, "syncwell_clients_total %d\n", clients)
	fmt.Fprintf(w, "syncwell_live_registers_total %d\n", live)
	fmt.Fprintf(w, "syncwell_tombstones_total %d\n", tombstones)
	fmt.Fprintln(w, "# HELP syncwell_rooms_low_water_stuck Rooms with clients and no acknowledgement watermark")
	fmt.Fprintln(w, "# TYPE syncwell_rooms_low_water_stuck gauge")
	fmt.Fprintf(w, "syncwell_rooms_low_water_stuck %d\n", lowWaterStuck)
	fmt.Fprintln(w, "# HELP syncwell_connections_current Current admitted WebSocket connections")
	fmt.Fprintln(w, "# TYPE syncwell_connections_current gauge")
	fmt.Fprintf(w, "syncwell_connections_current %d\n", h.connectionsCurrent.Load())
	fmt.Fprintln(w, "# HELP syncwell_connections_limit Process-wide WebSocket connection limit (0 means unlimited)")
	fmt.Fprintln(w, "# TYPE syncwell_connections_limit gauge")
	fmt.Fprintf(w, "syncwell_connections_limit %d\n", h.cfg.MaxConns)
	fmt.Fprintln(w, "# HELP syncwell_connection_rejects_total WebSocket connections rejected by the process-wide limit")
	fmt.Fprintln(w, "# TYPE syncwell_connection_rejects_total counter")
	fmt.Fprintf(w, "syncwell_connection_rejects_total %d\n", h.connectionRejects.Load())
	fmt.Fprintln(w, "# HELP syncwell_command_queue_rejects_total Commands rejected because room queues were full")
	fmt.Fprintln(w, "# TYPE syncwell_command_queue_rejects_total counter")
	fmt.Fprintf(w, "syncwell_command_queue_rejects_total %d\n", h.commandQueueRejects.Load())
	fmt.Fprintln(w, "# HELP syncwell_token_in_query_total WebSocket requests carrying a room token in the query string")
	fmt.Fprintln(w, "# TYPE syncwell_token_in_query_total counter")
	fmt.Fprintf(w, "syncwell_token_in_query_total %d\n", h.tokenInQuery.Load())
	if persistence, ok := h.cfg.Store.(interface {
		Stats() (store.Stats, error)
	}); ok {
		if ps, err := persistence.Stats(); err == nil {
			fmt.Fprintln(w, "# HELP syncwell_open_log_files Open room-log append descriptors")
			fmt.Fprintln(w, "# TYPE syncwell_open_log_files gauge")
			fmt.Fprintf(w, "syncwell_open_log_files %d\n", ps.OpenFiles)
			fmt.Fprintln(w, "# HELP syncwell_open_log_bytes Total persistent room-log bytes")
			fmt.Fprintln(w, "# TYPE syncwell_open_log_bytes gauge")
			fmt.Fprintf(w, "syncwell_open_log_bytes %d\n", ps.Bytes)
		}
	}
	openFDs, fdLimit := processFDStats()
	fmt.Fprintln(w, "# HELP syncwell_process_open_fds Process open file descriptors (Linux; 0 elsewhere)")
	fmt.Fprintln(w, "# TYPE syncwell_process_open_fds gauge")
	fmt.Fprintf(w, "syncwell_process_open_fds %d\n", openFDs)
	fmt.Fprintln(w, "# HELP syncwell_process_fd_limit Process soft file descriptor limit (Linux; 0 elsewhere)")
	fmt.Fprintln(w, "# TYPE syncwell_process_fd_limit gauge")
	fmt.Fprintf(w, "syncwell_process_fd_limit %d\n", fdLimit)
	fmt.Fprintln(w, "# HELP syncwell_goroutines Current Go goroutine count")
	fmt.Fprintln(w, "# TYPE syncwell_goroutines gauge")
	fmt.Fprintf(w, "syncwell_goroutines %d\n", runtime.NumGoroutine())
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	fmt.Fprintln(w, "# HELP syncwell_heap_bytes Current Go heap allocation")
	fmt.Fprintln(w, "# TYPE syncwell_heap_bytes gauge")
	fmt.Fprintf(w, "syncwell_heap_bytes %d\n", mem.HeapAlloc)
	fmt.Fprintln(w, "# HELP syncwell_gomemlimit_bytes Go runtime memory limit (0 when unlimited)")
	fmt.Fprintln(w, "# TYPE syncwell_gomemlimit_bytes gauge")
	memLimit := debug.SetMemoryLimit(-1)
	if memLimit < 0 {
		memLimit = 0
	}
	fmt.Fprintf(w, "syncwell_gomemlimit_bytes %d\n", memLimit)
	fmt.Fprintln(w, "# HELP syncwell_compaction_failures_total Room snapshot compaction failures")
	fmt.Fprintln(w, "# TYPE syncwell_compaction_failures_total counter")
	fmt.Fprintf(w, "syncwell_compaction_failures_total %d\n", h.compactionFailures.Load())
	fmt.Fprintln(w, "# HELP syncwell_anti_entropy_bytes_total Bytes sent in anti-entropy snapshots")
	fmt.Fprintln(w, "# TYPE syncwell_anti_entropy_bytes_total counter")
	fmt.Fprintf(w, "syncwell_anti_entropy_bytes_total %d\n", h.antiEntropyBytes.Load())

	h.metrics.applyDuration.write(w, "syncwell_apply_duration_seconds", "Time to merge and persist one register (local, merge, or cluster write)")
	h.metrics.inboundFrameBytes.write(w, "syncwell_inbound_frame_bytes", "WebSocket message size received from clients")
	h.metrics.outboundFrameBytes.write(w, "syncwell_outbound_frame_bytes", "WebSocket message size sent to each client")
	h.metrics.redisReplay.write(w, "syncwell_redis_replay_duration_seconds", "Redis snapshot load and ordered stream replay duration")
	fmt.Fprintln(w, "# HELP syncwell_redis_leases_current Redis room leases currently held by this process")
	fmt.Fprintln(w, "# TYPE syncwell_redis_leases_current gauge")
	fmt.Fprintf(w, "syncwell_redis_leases_current %d\n", h.redisLeasesCurrent.Load())
	fmt.Fprintln(w, "# HELP syncwell_redis_lease_acquired_total Redis room leases acquired")
	fmt.Fprintln(w, "# TYPE syncwell_redis_lease_acquired_total counter")
	fmt.Fprintf(w, "syncwell_redis_lease_acquired_total %d\n", h.redisLeaseAcquired.Load())
	fmt.Fprintln(w, "# HELP syncwell_redis_rehomes_total Rooms acquired after a prior fencing epoch")
	fmt.Fprintln(w, "# TYPE syncwell_redis_rehomes_total counter")
	fmt.Fprintf(w, "syncwell_redis_rehomes_total %d\n", h.redisRehomes.Load())
	fmt.Fprintln(w, "# HELP syncwell_redis_lease_losses_total Redis room lease losses")
	fmt.Fprintln(w, "# TYPE syncwell_redis_lease_losses_total counter")
	fmt.Fprintf(w, "syncwell_redis_lease_losses_total %d\n", h.redisLeaseLosses.Load())
	fmt.Fprintln(w, "# HELP syncwell_redis_stream_errors_total Redis stream, snapshot, or lease operation errors")
	fmt.Fprintln(w, "# TYPE syncwell_redis_stream_errors_total counter")
	fmt.Fprintf(w, "syncwell_redis_stream_errors_total %d\n", h.redisStreamErrors.Load())

	fmt.Fprintln(w, "# HELP syncwell_cluster_node_info Identity of this node (always 1)")
	fmt.Fprintln(w, "# TYPE syncwell_cluster_node_info gauge")
	fmt.Fprintf(w, "syncwell_cluster_node_info{node=%q} 1\n", h.nodeID)
	fmt.Fprintln(w, "# HELP syncwell_cluster_events_ingested_total Peer events delivered to local rooms")
	fmt.Fprintln(w, "# TYPE syncwell_cluster_events_ingested_total counter")
	fmt.Fprintf(w, "syncwell_cluster_events_ingested_total %d\n", h.clusterIngested.Load())
	fmt.Fprintln(w, "# HELP syncwell_cluster_events_dropped_total Peer events dropped (room cap, closed, or queue full)")
	fmt.Fprintln(w, "# TYPE syncwell_cluster_events_dropped_total counter")
	fmt.Fprintf(w, "syncwell_cluster_events_dropped_total %d\n", h.clusterDropped.Load())

	type relayMetrics interface {
		Metrics() (posted, dropped, errs, inflight uint64)
	}
	if rm, ok := h.relay.(relayMetrics); ok {
		posted, dropped, _, inflight := rm.Metrics()
		fmt.Fprintln(w, "# HELP syncwell_cluster_relay_posts_total Successful posts to peers")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_posts_total counter")
		fmt.Fprintf(w, "syncwell_cluster_relay_posts_total %d\n", posted)
		fmt.Fprintln(w, "# HELP syncwell_cluster_relay_dropped_total Events dropped before delivery (queue full)")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_dropped_total counter")
		fmt.Fprintf(w, "syncwell_cluster_relay_dropped_total %d\n", dropped)
		fmt.Fprintln(w, "# HELP syncwell_cluster_relay_inflight Events queued but not yet posted (lag proxy)")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_inflight gauge")
		fmt.Fprintf(w, "syncwell_cluster_relay_inflight %d\n", inflight)
	}
	if rm, ok := h.relay.(interface{ WritePeerMetrics(io.Writer) }); ok {
		rm.WritePeerMetrics(w)
	}

	if h.webhooks != nil {
		delivered, failed, dropped := h.webhooks.Metrics()
		fmt.Fprintln(w, "# HELP syncwell_webhook_delivered_total Successful webhook deliveries")
		fmt.Fprintln(w, "# TYPE syncwell_webhook_delivered_total counter")
		fmt.Fprintf(w, "syncwell_webhook_delivered_total %d\n", delivered)
		fmt.Fprintln(w, "# HELP syncwell_webhook_failed_total Failed webhook deliveries")
		fmt.Fprintln(w, "# TYPE syncwell_webhook_failed_total counter")
		fmt.Fprintf(w, "syncwell_webhook_failed_total %d\n", failed)
		fmt.Fprintln(w, "# HELP syncwell_webhook_dropped_total Events dropped because the queue was full")
		fmt.Fprintln(w, "# TYPE syncwell_webhook_dropped_total counter")
		fmt.Fprintf(w, "syncwell_webhook_dropped_total %d\n", dropped)
	}

	type peerStatus interface{ PeerUp() map[string]bool }
	if ps, ok := h.relay.(peerStatus); ok {
		fmt.Fprintln(w, "# HELP syncwell_cluster_peer_up Whether the last post to each peer succeeded")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_peer_up gauge")
		for peer, up := range ps.PeerUp() {
			v := 0
			if up {
				v = 1
			}
			fmt.Fprintf(w, "syncwell_cluster_peer_up{peer=%q} %d\n", peer, v)
		}
	}
}

// ServeClusterEvent ingests a cluster-event POST from a peer process and
// delivers it to the matching local room, creating that room if necessary.
func (h *Hub) ServeClusterEvent(w http.ResponseWriter, req *http.Request) {
	if h.cfg.RedisRoomLog != nil {
		http.NotFound(w, req)
		return
	}
	if !h.Ready() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !originOK(req.Header.Get("Origin"), h.cfg.Origins) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// Cluster events can inject writes/presence into any room, so a peer must
	// authenticate like any other privileged caller.
	if !h.adminAllowed(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer req.Body.Close()
	ev, err := UnmarshalClusterEvent(body)
	if err != nil || ev.Room == "" || ev.Kind == "" || ev.From == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.ingestClusterEvent(ev); err != nil {
		LogRequestEvent(req, "cluster_ingest_error", map[string]any{"room": ev.Room, "kind": ev.Kind, "from": ev.From, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ServeRoute returns the currently advertised WebSocket endpoint for a room.
// In Redis mode the lease owner is authoritative; relay mode uses the static
// planner. Callers must still handle an ownership change between routing and
// the subsequent WebSocket handshake.
func (h *Hub) ServeRoute(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	room := req.URL.Query().Get("room")
	if room == "" {
		room = "default"
	}
	if room == "*" || len(room) > h.cfg.Limits.MaxRoomIDBytes {
		http.Error(w, "invalid room", http.StatusBadRequest)
		return
	}
	owner := ""
	if h.cfg.RedisRoomLog != nil {
		ctx, cancel := context.WithTimeout(req.Context(), time.Second)
		defer cancel()
		found, err := h.cfg.RedisRoomLog.Owner(ctx, room)
		if err == nil {
			owner = found
		} else if !errors.Is(err, redis.Nil) {
			http.Error(w, "room route unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	if owner == "" && h.planner != nil {
		owner = h.planner.Owner(room)
	}
	if owner == "" {
		owner = h.nodeID
	}
	endpoint := h.cfg.NodeAdvertise[owner]
	if endpoint == "" && owner == h.nodeID {
		endpoint = h.cfg.NodeAdvertise[h.nodeID]
	}
	if endpoint == "" {
		http.Error(w, "room owner is not advertised", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Node string `json:"node"`
		URL  string `json:"url"`
	}{Node: owner, URL: endpoint})
}

// ServeClusterDigest answers a bounded per-register digest exchange request.
// Oversized exchanges are marked for the caller's snapshot fallback.
func (h *Hub) ServeClusterDigest(w http.ResponseWriter, req *http.Request) {
	if h.cfg.RedisRoomLog != nil {
		http.NotFound(w, req)
		return
	}
	if !h.Ready() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !originOK(req.Header.Get("Origin"), h.cfg.Origins) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	if !h.adminAllowed(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, (1<<20)+1))
	_ = req.Body.Close()
	if err != nil || len(body) > 1<<20 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var digestReq clusterDigestRequest
	if err := json.Unmarshal(body, &digestReq); err != nil || digestReq.Room == "" || digestReq.From == "" || len(digestReq.Digest) != 64 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, err := hex.DecodeString(digestReq.Digest); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r, err := h.room(digestReq.Room)
	if err != nil {
		http.Error(w, "room unavailable", http.StatusServiceUnavailable)
		return
	}
	result := make(chan map[string]crdt.Register, 1)
	select {
	case r.cmds <- command{kind: "export", export: result}:
	case <-r.done:
		http.Error(w, "room closed", http.StatusServiceUnavailable)
		return
	case <-req.Context().Done():
		return
	default:
		http.Error(w, "room busy", http.StatusServiceUnavailable)
		return
	}
	select {
	case local := <-result:
		if local == nil {
			http.Error(w, "room state unavailable", http.StatusServiceUnavailable)
			return
		}
		page := digestPage(local, digestReq.Cursor)
		response := clusterDigestResponse{Digest: DigestRegisters(local)}
		response.Entries = digestEntries(page)
		for key, reg := range page {
			if digestReq.Entries[key] != digestEntry(reg) {
				response.Missing = ensureRegisterMap(response.Missing)
				response.Missing[key] = reg
			}
		}
		encoded, err := json.Marshal(response)
		if err != nil || len(encoded) > 1<<20 {
			response = clusterDigestResponse{Digest: DigestRegisters(local), TooLarge: true}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	case <-r.done:
		http.Error(w, "room closed", http.StatusServiceUnavailable)
	case <-req.Context().Done():
	}
}

func ensureRegisterMap(m map[string]crdt.Register) map[string]crdt.Register {
	if m == nil {
		return make(map[string]crdt.Register)
	}
	return m
}

// errClusterQueueFull means a room's command queue is saturated; the event is
// dropped rather than blocking the peer (anti-entropy will reconcile later).
var errClusterQueueFull = errors.New("cluster event queue full")

// ingestClusterEvent delivers a peer event to the local room, creating the room
// if needed so state replicates even before a local client joins. Events
// originated by this node are ignored: they were applied when the client wrote
// them, and applying them twice would duplicate presence fan-out.
func (h *Hub) ingestClusterEvent(ev ClusterEvent) error {
	if ev.From == h.nodeID {
		return nil
	}
	r, err := h.room(ev.Room)
	if err != nil {
		h.clusterDropped.Add(1)
		return err
	}
	select {
	case r.cmds <- command{kind: "cluster", cluster: &ev}:
		h.clusterIngested.Add(1)
		return nil
	case <-r.done:
		h.clusterDropped.Add(1)
		return errors.New("room closed")
	default:
		h.clusterDropped.Add(1)
		return errClusterQueueFull
	}
}

// ServeReady reports whether the hub is still accepting realtime traffic.
func (h *Hub) ServeReady(w http.ResponseWriter, _ *http.Request) {
	if !h.Ready() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// verifyToken accepts the current secret and any previous secrets kept during a
// rotation window, so tokens minted before a rotation keep working until they
// expire.
func (h *Hub) verifyToken(room, token string, now int64) (TokenClaims, bool) {
	if token == "" {
		return TokenClaims{}, false
	}
	if h.cfg.Secret != "" {
		if claims, ok := VerifyAccessToken(h.cfg.Secret, room, token, now); ok {
			return claims, true
		}
		for _, prev := range h.cfg.PreviousSecrets {
			if prev == "" || prev == h.cfg.Secret {
				continue
			}
			if claims, ok := VerifyAccessToken(prev, room, token, now); ok {
				return claims, true
			}
		}
	}
	if h.cfg.OIDC != nil {
		if claims, ok := h.cfg.OIDC.Verify(token, room, time.Unix(now, 0)); ok {
			return claims, true
		}
	}
	return TokenClaims{}, false
}

func (h *Hub) adminAllowed(req *http.Request) bool {
	if h.cfg.AdminToken == "" && h.cfg.Secret == "" && h.cfg.OIDC == nil {
		return true
	}
	got := ""
	if auth := req.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		got = strings.TrimPrefix(auth, "Bearer ")
	}
	// Tokens are accepted only via the Authorization header. Query-string
	// credentials leak into access logs, proxies, and browser history.
	if got != "" && (h.cfg.Secret != "" || h.cfg.OIDC != nil) {
		claims, ok := h.verifyToken("*", got, time.Now().Unix())
		if ok && claims.Can(CapAdmin) {
			if _, revoked := h.cfg.RevokedJTIs[claims.JTI]; revoked && claims.JTI != "" {
				return false
			}
			return true
		}
	}
	if h.cfg.AdminToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.cfg.AdminToken)) == 1
}

// websocketToken extracts a room token without requiring it in the query
// string. Precedence: Authorization: Bearer, then Sec-WebSocket-Protocol
// ("syncwell.token.<token>"), then the legacy ?token= parameter. The returned
// protocol value is echoed in the handshake response so standards-compliant
// clients (including browsers) see their offered protocol accepted.
func websocketToken(req *http.Request) (token, protocol string) {
	if auth := req.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	selected := ""
	for _, raw := range strings.Split(req.Header.Get("Sec-WebSocket-Protocol"), ",") {
		p := strings.TrimSpace(raw)
		switch {
		case p == "syncwell":
			selected = "syncwell"
		case strings.HasPrefix(p, "syncwell.token."):
			if token == "" {
				token = strings.TrimPrefix(p, "syncwell.token.")
			}
			if selected == "" {
				selected = p
			}
		}
	}
	if token == "" {
		token = req.URL.Query().Get("token")
	}
	return strings.TrimSpace(token), selected
}

// ServeWS upgrades an HTTP request to a websocket and attaches it to the room
// named by ?room=ID (default "default").
func (h *Hub) ServeWS(w http.ResponseWriter, req *http.Request) {
	if req.URL.Query().Get("token") != "" {
		h.tokenInQuery.Add(1)
		now := time.Now().UnixNano()
		last := h.lastTokenQueryLog.Load()
		if now-last >= int64(time.Minute) && h.lastTokenQueryLog.CompareAndSwap(last, now) {
			LogRequestEvent(req, "ws_token_query_deprecated", map[string]any{"room": h.logRoomID(req.URL.Query().Get("room"))})
		}
	}
	if !h.Ready() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	roomID := req.URL.Query().Get("room")
	if roomID == "" {
		roomID = "default"
	}
	// "*" is the admin-token scope, not a real collaboration room. Refuse it
	// at the websocket edge so a wildcard OIDC token can never join a literal
	// room named "*" and read every room's shared document.
	if roomID == "*" {
		http.Error(w, "invalid room", http.StatusBadRequest)
		return
	}
	if len(roomID) > h.cfg.Limits.MaxRoomIDBytes {
		http.Error(w, "room id too large", http.StatusRequestEntityTooLarge)
		return
	}
	_, span := otel.Tracer("syncwell").Start(req.Context(), "ws.join",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("syncwell.room", h.logRoomID(roomID))),
	)
	defer span.End()
	if h.planner != nil {
		owner := h.planner.Owner(roomID)
		if owner != "" && owner != h.nodeID {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMisdirectedRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "misdirected request",
				"owner": owner,
			})
			return
		}
	}
	// Origin allow-list (when configured).
	if !h.originAllowed(req.Header.Get("Origin")) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// Room token (when a secret or OIDC is configured). Open dev mode gets full caps.
	// Authorization and the Sec-WebSocket-Protocol carrier avoid putting
	// credentials in the URL, where proxies and access logs would capture them.
	token, wsProtocol := websocketToken(req)
	var claims TokenClaims
	if h.cfg.Secret != "" || h.cfg.OIDC != nil {
		var ok bool
		claims, ok = h.verifyToken(roomID, token, time.Now().Unix())
		if !ok || !claims.Can(CapRead) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, revoked := h.cfg.RevokedJTIs[claims.JTI]; revoked && claims.JTI != "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	} else {
		claims = TokenClaims{Caps: map[string]bool{CapRead: true, CapWrite: true, CapPresence: true}}
	}

	// Admission control happens before the upgrade so capacity cannot leak a
	// socket. A room created for an upgrade that then fails is idle-evicted.
	nodeID := strings.TrimSpace(req.URL.Query().Get("client"))
	if len(nodeID) > h.cfg.Limits.MaxNodeIDBytes {
		http.Error(w, "client id too large", http.StatusBadRequest)
		return
	}
	if !h.acquireConnection() {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "connection capacity reached", http.StatusServiceUnavailable)
		return
	}
	reserved := true
	defer func() {
		if reserved {
			h.connectionsCurrent.Add(-1)
		}
	}()
	room, err := h.room(roomID)
	if err != nil {
		if errors.Is(err, errRedisLeaseHeld) {
			ctx, cancel := context.WithTimeout(req.Context(), time.Second)
			owner, ownerErr := h.cfg.RedisRoomLog.Owner(ctx, roomID)
			cancel()
			if ownerErr == nil {
				if endpoint := h.cfg.NodeAdvertise[owner]; endpoint != "" {
					w.Header().Set("X-Syncwell-Owner", owner)
					w.Header().Set("Location", endpoint)
					w.WriteHeader(http.StatusMisdirectedRequest)
					return
				}
			}
		}
		http.Error(w, "room capacity reached", http.StatusServiceUnavailable)
		return
	}
	responseHeader := http.Header{}
	if wsProtocol != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", wsProtocol)
	}
	conn, err := upgrader.Upgrade(w, req, responseHeader)
	if err != nil {
		span.SetStatus(codes.Error, "upgrade failed")
		LogRequestEvent(req, "ws_upgrade_error", map[string]any{"error": err.Error(), "room": h.logRoomID(roomID)})
		return
	}
	span.SetAttributes(attribute.Int("syncwell.caps", len(claims.Caps)))
	client := &Client{
		id:            nextClientID(),
		room:          room,
		send:          make(chan []byte, 64),
		claims:        claims,
		node:          nodeID,
		delta:         req.URL.Query().Get("delta") == "1",
		maxQueueBytes: h.cfg.Limits.MaxSendBytes,
		hub:           h,
	}
	reserved = false
	LogRequestEvent(req, "ws_join", map[string]any{
		"room":      h.logRoomID(roomID),
		"client":    client.id,
		"sub":       claims.Sub,
		"caps":      claims.Caps,
		"has_admin": claims.Can(CapAdmin),
	})
	if !enqueueRoomCommand(room, command{kind: "join", client: client}) {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "room is busy"), time.Now().Add(writeWait))
		client.releaseConnection()
		_ = conn.Close()
		return
	}
	go client.writePump(conn)
	go client.readPump(conn)
}

// readPump reads frames from the socket and forwards intents to the room. It
// never closes client.send (that is the room goroutine's job) — on exit it just
// enqueues a leave command and closes the socket.
func (c *Client) readPump(conn *websocket.Conn) {
	defer func() {
		c.releaseConnection()
		_ = enqueueRoomCommand(c.room, command{kind: "leave", client: c})
		conn.Close()
	}()
	conn.SetReadLimit(int64(c.room.limits.MaxMessageBytes))
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	limiter := newMessageRateLimiter(c.room.limits.MaxMessagesPerSecond)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !limiter.allow(time.Now()) {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "rate limit"),
				time.Now().Add(writeWait),
			)
			return
		}
		var in Inbound
		if err := json.Unmarshal(data, &in); err != nil {
			continue // ignore malformed frames rather than dropping the client
		}
		if err := validateInbound(in, c.room.limits); err != nil {
			_ = conn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, err.Error()),
				time.Now().Add(writeWait),
			)
			return
		}
		if c.room.hub != nil && c.room.hub.metrics != nil {
			c.room.hub.metrics.inboundFrameBytes.observe(float64(len(data)))
		}
		switch in.Type {
		case "op", "merge", "hello":
			if !enqueueRoomCommand(c.room, command{kind: in.Type, client: c, in: in}) {
				c.closeBusy(conn)
				return
			}
		case "presence", "ack":
			if !enqueueRoomCommand(c.room, command{kind: in.Type, client: c, in: in}) {
				c.closeBusy(conn)
				return
			}
		}
	}
}

func enqueueRoomCommand(r *Room, c command) bool {
	select {
	case <-r.done:
		return false
	default:
	}
	select {
	case r.cmds <- c:
		return true
	case <-r.done:
		return false
	default:
		if r.hub != nil {
			r.hub.recordCommandQueueFull(r.id)
		}
		return false
	}
}

func (c *Client) closeBusy(conn *websocket.Conn) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "room is busy"), time.Now().Add(writeWait))
}

type messageRateLimiter struct {
	limit       int
	windowStart time.Time
	used        int
}

func newMessageRateLimiter(limit int) *messageRateLimiter {
	return &messageRateLimiter{limit: limit}
}

func (l *messageRateLimiter) allow(now time.Time) bool {
	if l.limit <= 0 {
		return true
	}
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= time.Second {
		l.windowStart = now
		l.used = 0
	}
	l.used++
	return l.used <= l.limit
}

func validateInbound(in Inbound, limits Limits) error {
	if in.Protocol != 0 && in.Protocol != protocolVersion {
		return errProtocol("unsupported protocol")
	}
	switch in.Type {
	case "op":
		if in.Key == "" || len(in.Key) > limits.MaxKeyBytes {
			return errProtocol("bad key")
		}
		if in.Register == nil {
			return errProtocol("missing register")
		}
		return validateRegister(*in.Register, limits)
	case "merge":
		if len(in.State) > limits.MaxStateEntries {
			return errProtocol("state too large")
		}
		for key, reg := range in.State {
			if key == "" || len(key) > limits.MaxKeyBytes {
				return errProtocol("bad key")
			}
			if err := validateRegister(reg, limits); err != nil {
				return err
			}
		}
		return nil
	case "hello":
		if len(in.Vector) > limits.MaxStateEntries {
			return errProtocol("vector too large")
		}
		for key, ts := range in.Vector {
			if key == "" || len(key) > limits.MaxKeyBytes {
				return errProtocol("bad key")
			}
			if ts.Node == "" || len(ts.Node) > limits.MaxNodeIDBytes {
				return errProtocol("bad node")
			}
		}
		if len(in.State) > limits.MaxStateEntries {
			return errProtocol("state too large")
		}
		for key, reg := range in.State {
			if key == "" || len(key) > limits.MaxKeyBytes {
				return errProtocol("bad key")
			}
			if err := validateRegister(reg, limits); err != nil {
				return err
			}
		}
		return nil
	case "presence":
		if len(in.Data) > limits.MaxPresenceBytes {
			return errProtocol("presence too large")
		}
		if len(in.Data) > 0 && !json.Valid(in.Data) {
			return errProtocol("bad presence")
		}
		return nil
	case "ack":
		return nil
	default:
		return errProtocol("bad type")
	}
}

func validateRegister(reg crdt.Register, limits Limits) error {
	if reg.TS.Node == "" || len(reg.TS.Node) > limits.MaxNodeIDBytes {
		return errProtocol("bad node")
	}
	if reg.Deleted {
		if len(reg.Value) > 0 {
			return errProtocol("tombstone must not carry a value")
		}
		return nil
	}
	if len(reg.Value) == 0 || len(reg.Value) > limits.MaxValueBytes || !json.Valid(reg.Value) {
		return errProtocol("bad value")
	}
	return nil
}

type errProtocol string

func (e errProtocol) Error() string { return string(e) }

// writePump drains client.send to the socket and sends periodic pings. When the
// room closes client.send, the pump flushes a close frame and exits.
func (c *Client) writePump(conn *websocket.Conn) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		c.releaseConnection()
		ticker.Stop()
		conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			err := conn.WriteMessage(websocket.TextMessage, msg)
			c.queuedBytes.Add(-int64(len(msg)))
			if err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func processFDStats() (int, int) {
	if runtime.GOOS != "linux" {
		return 0, 0
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, 0
	}
	open := len(entries)
	data, err := os.ReadFile("/proc/self/limits")
	if err != nil {
		return open, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Max open files") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] == "unlimited" {
			return open, 0
		}
		limit, err := strconv.Atoi(fields[3])
		if err != nil {
			return open, 0
		}
		return open, limit
	}
	return open, 0
}
