package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

//go:embed admin.html
var adminHTML []byte

// ServeAdminUI serves the static dashboard shell. It contains no room data;
// every data request is authenticated with a bearer admin token.
func (h *Hub) ServeAdminUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(adminHTML)
}

type adminRoom struct {
	ID string `json:"id"`
	RoomStats
}

// ServeAdminRooms returns the live room list as JSON for the dashboard.
func (h *Hub) ServeAdminRooms(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	rooms := make([]adminRoom, 0, len(stats))
	for id, s := range stats {
		rooms = append(rooms, adminRoom{ID: id, RoomStats: s})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"rooms": rooms, "node": h.nodeID})
}

// ServeAdminRoomAction performs an operator action on one room:
//
//	POST /admin/api/rooms/{room}/resnapshot
//	POST /admin/api/rooms/{room}/evict
func (h *Hub) ServeAdminRoomAction(w http.ResponseWriter, req *http.Request) {
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
	rest := strings.TrimPrefix(req.URL.Path, "/admin/api/rooms/")
	idx := strings.LastIndex(rest, "/")
	if idx <= 0 || idx == len(rest)-1 {
		http.Error(w, "bad room action", http.StatusBadRequest)
		return
	}
	room, action := rest[:idx], rest[idx+1:]
	var ok bool
	switch action {
	case "resnapshot":
		ok = h.ResnapshotRoomWithRequestID(room, RequestID(req.Context()))
	case "evict":
		ok = h.EvictRoomWithRequestID(room, RequestID(req.Context()))
	case "purge":
		err := h.PurgeRoom(room, RequestID(req.Context()))
		if err != nil {
			h.recordAudit(req, "room.purge", room, "error")
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		h.recordAudit(req, "room.purge", room, "ok")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "room": room, "action": action})
		return
	default:
		http.Error(w, "unknown action", http.StatusNotFound)
		return
	}
	if !ok {
		h.recordAudit(req, "room."+action, room, "error")
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	h.recordAudit(req, "room."+action, room, "ok")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "room": room, "action": action})
}
