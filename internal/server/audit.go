package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// AuditEvent records a privileged operation without credential or document data.
type AuditEvent struct {
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	IP        string `json:"ip"`
	Room      string `json:"room"`
	Result    string `json:"result"`
	RequestID string `json:"request_id"`
}

// AuditLogger writes structured audit events to stderr and optionally to a
// private append-only file. File writes are serialized and fsynced per event.
type AuditLogger struct {
	mu   sync.Mutex
	file *os.File
}

func OpenAuditLogger(path string) (*AuditLogger, error) {
	l := &AuditLogger{}
	if path == "" {
		return l, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	l.file = f
	return l, nil
}

func (l *AuditLogger) Record(event AuditEvent) error {
	if l == nil {
		return errors.New("audit logger is nil")
	}
	fields := map[string]any{
		"level":      "audit",
		"event":      "audit",
		"action":     event.Action,
		"actor":      event.Actor,
		"ip":         event.IP,
		"room":       event.Room,
		"result":     event.Result,
		"request_id": event.RequestID,
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	log.Print(string(data))
	if l.file == nil {
		return nil
	}
	if _, err := l.file.Write(append(data, '\n')); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *AuditLogger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (h *Hub) recordAudit(req *http.Request, action, room, result string) {
	actor := "admin-token"
	if req != nil {
		token := ""
		if auth := req.Header.Get("Authorization"); len(auth) >= len("Bearer ") && auth[:len("Bearer ")] == "Bearer " {
			token = auth[len("Bearer "):]
		}
		if h.cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.cfg.AdminToken)) == 1 {
			actor = "admin-token"
		} else if claims, ok := h.verifyToken("*", token, time.Now().Unix()); ok && claims.Sub != "" && h.cfg.OIDC != nil {
			actor = "oidc:" + claims.Sub
		}
	}
	event := AuditEvent{Action: action, Actor: actor, IP: "unknown", Room: h.logRoomID(room), Result: result}
	if req != nil {
		event.IP = h.clientIP(req)
		event.RequestID = RequestID(req.Context())
	}
	logger := h.cfg.AuditLogger
	if logger == nil {
		logger = &AuditLogger{}
	}
	if err := logger.Record(event); err != nil {
		LogEvent("audit_write_error", map[string]any{"action": action, "error": err.Error()})
	}
}

func (h *Hub) auditAdminFailure(req *http.Request, ip string) {
	now := time.Now()
	h.auditAuthMu.Lock()
	if h.auditAuthLast == nil {
		h.auditAuthLast = make(map[string]time.Time)
	}
	last := h.auditAuthLast[ip]
	if now.Sub(last) < time.Minute {
		h.auditAuthMu.Unlock()
		return
	}
	if len(h.auditAuthLast) >= 4096 {
		for key, seen := range h.auditAuthLast {
			if now.Sub(seen) >= time.Minute {
				delete(h.auditAuthLast, key)
			}
		}
		if len(h.auditAuthLast) >= 4096 {
			h.auditAuthMu.Unlock()
			return
		}
	}
	h.auditAuthLast[ip] = now
	h.auditAuthMu.Unlock()
	h.recordAudit(req, "admin.auth", "", "denied")
}
