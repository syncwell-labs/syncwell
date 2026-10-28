package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"syncwell/internal/crdt"

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
	var clients, live, tombstones uint64
	for _, s := range stats {
		clients += uint64(s.Clients)
		live += uint64(s.Live)
		tombstones += uint64(s.Tombstones)
	}
	fmt.Fprintf(w, "syncwell_clients_total %d\n", clients)
	fmt.Fprintf(w, "syncwell_live_registers_total %d\n", live)
	fmt.Fprintf(w, "syncwell_tombstones_total %d\n", tombstones)
	for room, s := range stats {
		fmt.Fprintf(w, "syncwell_room_clients{room=%q} %d\n", room, s.Clients)
		fmt.Fprintf(w, "syncwell_room_live{room=%q} %d\n", room, s.Live)
		fmt.Fprintf(w, "syncwell_room_tombstones{room=%q} %d\n", room, s.Tombstones)
		fmt.Fprintf(w, "syncwell_room_low_water{room=%q} %d\n", room, s.LowWater)
	}

	h.metrics.applyDuration.write(w, "syncwell_apply_duration_seconds", "Time to merge and persist one register (local, merge, or cluster write)")
	h.metrics.inboundFrameBytes.write(w, "syncwell_inbound_frame_bytes", "WebSocket message size received from clients")
	h.metrics.outboundFrameBytes.write(w, "syncwell_outbound_frame_bytes", "WebSocket message size sent to each client")

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
		posted, dropped, errs, inflight := rm.Metrics()
		fmt.Fprintln(w, "# HELP syncwell_cluster_relay_posts_total Successful posts to peers")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_posts_total counter")
		fmt.Fprintf(w, "syncwell_cluster_relay_posts_total %d\n", posted)
		fmt.Fprintln(w, "# HELP syncwell_cluster_relay_errors_total Failed peer posts")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_errors_total counter")
		fmt.Fprintf(w, "syncwell_cluster_relay_errors_total %d\n", errs)
		fmt.Fprintln(w, "# HELP syncwell_cluster_relay_dropped_total Events dropped before delivery (queue full)")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_dropped_total counter")
		fmt.Fprintf(w, "syncwell_cluster_relay_dropped_total %d\n", dropped)
		fmt.Fprintln(w, "# HELP syncwell_cluster_relay_inflight Events queued but not yet posted (lag proxy)")
		fmt.Fprintln(w, "# TYPE syncwell_cluster_relay_inflight gauge")
		fmt.Fprintf(w, "syncwell_cluster_relay_inflight %d\n", inflight)
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
		LogEvent("cluster_ingest_error", map[string]any{"room": ev.Room, "kind": ev.Kind, "from": ev.From, "error": err.Error()})
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
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
		trace.WithAttributes(attribute.String("syncwell.room", roomID)),
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
	room, err := h.room(roomID)
	if err != nil {
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
		LogEvent("ws_upgrade_error", map[string]any{"error": err.Error(), "room": roomID})
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
	}
	LogEvent("ws_join", map[string]any{
		"room":      roomID,
		"client":    client.id,
		"sub":       claims.Sub,
		"caps":      claims.Caps,
		"has_admin": claims.Can(CapAdmin),
	})
	room.cmds <- command{kind: "join", client: client}
	go client.writePump(conn)
	go client.readPump(conn)
}

// readPump reads frames from the socket and forwards intents to the room. It
// never closes client.send (that is the room goroutine's job) — on exit it just
// enqueues a leave command and closes the socket.
func (c *Client) readPump(conn *websocket.Conn) {
	defer func() {
		select {
		case c.room.cmds <- command{kind: "leave", client: c}:
		case <-c.room.done:
		}
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
			c.room.cmds <- command{kind: in.Type, client: c, in: in}
		case "presence", "ack":
			c.room.cmds <- command{kind: in.Type, client: c, in: in}
		}
	}
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
