package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

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
	maxMsg     = 1 << 20 // 1 MiB read cap per message
)

// ServeWS upgrades an HTTP request to a websocket and attaches it to the room
// named by ?room=ID (default "default").
func (h *Hub) ServeWS(w http.ResponseWriter, req *http.Request) {
	roomID := req.URL.Query().Get("room")
	if roomID == "" {
		roomID = "default"
	}
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("upgrade: %v", err)
		return
	}
	room := h.room(roomID)
	client := &Client{id: nextClientID(), room: room, send: make(chan []byte, 64)}
	room.cmds <- command{kind: "join", client: client}
	go client.writePump(conn)
	go client.readPump(conn)
}

// readPump reads frames from the socket and forwards intents to the room. It
// never closes client.send (that is the room goroutine's job) — on exit it just
// enqueues a leave command and closes the socket.
func (c *Client) readPump(conn *websocket.Conn) {
	defer func() {
		c.room.cmds <- command{kind: "leave", client: c}
		conn.Close()
	}()
	conn.SetReadLimit(maxMsg)
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var in Inbound
		if err := json.Unmarshal(data, &in); err != nil {
			continue // ignore malformed frames rather than dropping the client
		}
		switch in.Type {
		case "set", "delete":
			c.room.cmds <- command{kind: "op", client: c, in: in}
		case "presence":
			c.room.cmds <- command{kind: "presence", client: c, in: in}
		}
	}
}

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
