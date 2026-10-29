package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAuditLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid audit JSON %q: %v", scanner.Text(), err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestAuditLoggerWritesMode0600StructuredEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := OpenAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(AuditEvent{Action: "room.purge", Actor: "admin-token", IP: "192.0.2.8", Room: "room-a", Result: "ok", RequestID: "request-1234"}); err != nil {
		t.Fatal(err)
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("audit file mode = %04o, want 0600", info.Mode().Perm())
	}
	events := readAuditLines(t, path)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	for key, want := range map[string]string{
		"level": "audit", "event": "audit", "action": "room.purge", "actor": "admin-token",
		"ip": "192.0.2.8", "room": "room-a", "result": "ok", "request_id": "request-1234",
	} {
		if got := events[0][key]; got != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
	if strings.Contains(string(mustReadFile(t, path)), "secret") {
		t.Fatal("audit event unexpectedly contains a credential substring")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAdminAuthFailuresAreAuditedAndRateLimited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := OpenAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	h := NewHub(Config{AdminToken: "expected-secret", AuditLogger: logger, AdminRatePerMinute: 600, AdminBurst: 100})
	handler := TracingMiddleware(h.AdminHandler(func(w http.ResponseWriter, req *http.Request) {
		if !h.adminAllowed(req) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	for range 3 {
		req := httptest.NewRequest(http.MethodGet, "/stats", nil)
		req.RemoteAddr = "192.0.2.9:1200"
		req.Header.Set("X-Request-ID", "request-5678")
		req.Header.Set("Authorization", "Bearer wrong-secret")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	events := readAuditLines(t, path)
	if len(events) != 1 {
		t.Fatalf("got %d auth audit events, want one rate-limited event", len(events))
	}
	if events[0]["action"] != "admin.auth" || events[0]["result"] != "denied" || events[0]["request_id"] != "request-5678" {
		t.Fatalf("unexpected auth audit event: %+v", events[0])
	}
	if strings.Contains(string(mustReadFile(t, path)), "wrong-secret") {
		t.Fatal("audit event contains the rejected credential")
	}
}

func TestAdminActionCarriesRequestIDIntoRoomLoopAndAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := OpenAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	h := NewHub(Config{AdminToken: "admin-secret", AuditLogger: logger})
	if _, err := h.room("room-a"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	handler := TracingMiddleware(h.AdminHandler(h.ServeAdminRoomAction))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/rooms/room-a/resnapshot", nil)
	req.RemoteAddr = "192.0.2.10:1200"
	req.Header.Set("X-Request-ID", "request-9012")
	req.Header.Set("Authorization", "Bearer admin-secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("resnapshot status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(logs.String(), `"event":"room_resnapshot"`) || !strings.Contains(logs.String(), `"request_id":"request-9012"`) {
		t.Fatalf("room loop event lacks request id: %s", logs.String())
	}
	events := readAuditLines(t, path)
	if len(events) != 1 || events[0]["action"] != "room.resnapshot" || events[0]["request_id"] != "request-9012" {
		t.Fatalf("unexpected action audit events: %+v", events)
	}
}

func TestAdminEvictIsAuditedWithRequestID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := OpenAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	h := NewHub(Config{AdminToken: "admin-secret", AuditLogger: logger})
	if _, err := h.room("room-b"); err != nil {
		t.Fatal(err)
	}
	handler := TracingMiddleware(h.AdminHandler(h.ServeAdminRoomAction))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/rooms/room-b/evict", nil)
	req.Header.Set("X-Request-ID", "request-evict")
	req.Header.Set("Authorization", "Bearer admin-secret")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("evict status = %d: %s", rr.Code, rr.Body.String())
	}
	events := readAuditLines(t, path)
	if len(events) != 1 || events[0]["action"] != "room.evict" || events[0]["request_id"] != "request-evict" || events[0]["result"] != "ok" {
		t.Fatalf("unexpected evict audit event: %+v", events)
	}
}

func TestRedisModeAuditHashesRoomID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := OpenAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	room := "sensitive-customer-room"
	h := NewHub(Config{RedisRoomLog: &RedisRoomLog{}, AuditLogger: logger})
	h.recordAudit(nil, "room.purge", room, "ok")

	events := readAuditLines(t, path)
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want one", len(events))
	}
	want := redisRoomHash(room)
	if events[0]["room"] != want {
		t.Fatalf("audit room = %v, want hash %q", events[0]["room"], want)
	}
	if strings.Contains(string(mustReadFile(t, path)), room) {
		t.Fatal("audit output contains raw Redis-mode room ID")
	}
}
