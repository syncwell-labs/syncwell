package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"syncwell/internal/crdt"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// Dev default: accept any origin. Lock this down behind an all‑list in
	// production (see docs/ROADMAP.md → auth hooks).
	CheckOrigin: func(*http.Request) bool { return true },
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
	return l
}

// ServeStats writes per-room statistics as JSON (live keys, tombstones, clients,
// GC low-water mark).
func (h *Hub) ServeStats(w http.ResponseWriter, req *http.Request) {
	if !h.adminAllowed(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.Stats())
}

// ServeMetrics writes a tiny Prometheus-style text snapshot of the current room
// set. It reuses the same admin-token protection as /stats.
func (h *Hub) ServeMetrics(w http.ResponseWriter, req *http.Request) {
	if !h.adminAllowed(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	stats := h.Stats()
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
}

// ServeClusterEvent ingests a cluster-event POST from a peer process and
// republishes it to the local in-process bus.
func (h *Hub) ServeClusterEvent(w http.ResponseWriter, req *http.Request) {
	if !h.Ready() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Cluster events can inject writes/presence into any room, so a peer must
	// authenticate like any other privileged caller.
	if !h.adminAllowed(req) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.bus == nil {
		http.Error(w, "cluster bus unavailable", http.StatusServiceUnavailable)
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
	h.bus.Publish(ev)
	w.WriteHeader(http.StatusAccepted)
}

// ServeReady reports whether the hub is still accepting realtime traffic.
func (h *Hub) ServeReady(w http.ResponseWriter, _ *http.Request) {
	if !h.Ready() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

func (h *Hub) adminAllowed(req *http.Request) bool {
	if h.cfg.AdminToken == "" {
		if h.cfg.Secret == "" {
			return true
		}
	}
	got := ""
	if auth := req.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		got = strings.TrimPrefix(auth, "Bearer ")
	}
	// Tokens are accepted only via the Authorization header. Query-string
	// credentials leak into access logs, proxies, and browser history.
	if got != "" && h.cfg.Secret != "" {
		claims, ok := VerifyAccessToken(h.cfg.Secret, "*", got, time.Now().Unix())
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
	if len(roomID) > h.cfg.Limits.MaxRoomIDBytes {
		http.Error(w, "room id too large", http.StatusRequestEntityTooLarge)
		return
	}
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
	// Room token (when a secret is configured). Open dev mode gets full caps.
	var claims TokenClaims
	if h.cfg.Secret != "" {
		var ok bool
		claims, ok = VerifyAccessToken(h.cfg.Secret, roomID, req.URL.Query().Get("token"), time.Now().Unix())
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
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		LogEvent("ws_upgrade_error", map[string]any{"error": err.Error(), "room": roomID})
		return
	}
	client := &Client{id: nextClientID(), room: room, send: make(chan []byte, 64), claims: claims, node: nodeID}
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
		switch in.Type {
		case "op", "merge":
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
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
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
