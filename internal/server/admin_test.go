package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"syncwell/internal/crdt"
)

func TestAdminAllowedOpenWhenNoTokenConfigured(t *testing.T) {
	h := NewHub(Config{})
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	if !h.adminAllowed(req) {
		t.Fatal("admin endpoint should be open when no admin token is configured")
	}
}

func TestAdminAllowedRequiresBearerToken(t *testing.T) {
	h := NewHub(Config{AdminToken: "secret-admin"})

	none := httptest.NewRequest(http.MethodGet, "/stats", nil)
	if h.adminAllowed(none) {
		t.Fatal("missing admin token accepted")
	}

	badScheme := httptest.NewRequest(http.MethodGet, "/stats", nil)
	badScheme.Header.Set("Authorization", "Basic secret-admin")
	if h.adminAllowed(badScheme) {
		t.Fatal("non-bearer Authorization value accepted")
	}

	bearer := httptest.NewRequest(http.MethodGet, "/stats", nil)
	bearer.Header.Set("Authorization", "Bearer secret-admin")
	if !h.adminAllowed(bearer) {
		t.Fatal("valid bearer admin token rejected")
	}

	// Query-string credentials are deliberately rejected (leak risk).
	query := httptest.NewRequest(http.MethodGet, "/stats?admin_token=secret-admin", nil)
	if h.adminAllowed(query) {
		t.Fatal("query admin token accepted; only the Authorization header is allowed")
	}
}

func TestAdminAllowedAcceptsAdminCapabilityToken(t *testing.T) {
	const secret = "s3cret"
	h := NewHub(Config{Secret: secret})
	tok := SignAccessToken(secret, "*", time.Now().Add(time.Minute).Unix(), []string{CapAdmin}, "admin-jti")

	bearer := httptest.NewRequest(http.MethodGet, "/stats", nil)
	bearer.Header.Set("Authorization", "Bearer "+tok)
	if !h.adminAllowed(bearer) {
		t.Fatal("valid admin capability token rejected")
	}
}

func TestAdminAllowedRejectsNonAdminCapabilityToken(t *testing.T) {
	const secret = "s3cret"
	h := NewHub(Config{Secret: secret})
	tok := SignAccessToken(secret, "*", 2000, []string{CapRead}, "read-jti")

	bearer := httptest.NewRequest(http.MethodGet, "/stats", nil)
	bearer.Header.Set("Authorization", "Bearer "+tok)
	if h.adminAllowed(bearer) {
		t.Fatal("non-admin capability token accepted")
	}
}

func TestVerifyTokenAcceptsPreviousSecret(t *testing.T) {
	h := NewHub(Config{Secret: "new-secret", PreviousSecrets: []string{"old-secret"}})
	now := time.Now().Unix()
	tok := SignAccessToken("old-secret", "room-1", now+60, []string{CapRead}, "jti")

	claims, ok := h.verifyToken("room-1", tok, now)
	if !ok || claims.Room != "room-1" {
		t.Fatal("token signed with the previous secret should be accepted")
	}
	if _, ok := h.verifyToken("room-2", tok, now); ok {
		t.Fatal("previous-secret token must not cross rooms")
	}
	if _, ok := h.verifyToken("room-1", tok, now+120); ok {
		t.Fatal("expired token accepted")
	}
	unknown := SignAccessToken("other-secret", "room-1", now+60, []string{CapRead}, "")
	if _, ok := h.verifyToken("room-1", unknown, now); ok {
		t.Fatal("token signed with an unknown secret accepted")
	}
}

func TestConfigValidateSecretRotation(t *testing.T) {
	if err := (Config{PreviousSecrets: []string{"old"}}).Validate(); err == nil {
		t.Fatal("previous secrets without a current secret should be rejected")
	}
	if err := (Config{Secret: "new", PreviousSecrets: []string{"old"}}).Validate(); err != nil {
		t.Fatalf("valid rotation config rejected: %v", err)
	}
}

func TestAdminRoomStateAndSnapshotExport(t *testing.T) {
	h := NewHub(Config{AdminToken: "admin-pass"})
	room, err := h.room("export-room")
	if err != nil {
		t.Fatalf("failed to create room: %v", err)
	}
	// Seed state via cluster snapshot command
	room.cmds <- command{
		kind: "cluster",
		cluster: &ClusterEvent{
			Room: "export-room",
			Kind: "snapshot",
			From: "remote-node",
			State: map[string]crdt.Register{
				"title": {
					Value: json.RawMessage(`"Collaborative Doc"`),
					TS:    crdt.Timestamp{Counter: 1, Node: "remote-node"},
				},
			},
		},
	}
	// Wait a moment for goroutine to process op
	time.Sleep(50 * time.Millisecond)

	// Test GET /admin/api/rooms/export-room/state
	reqState := httptest.NewRequest(http.MethodGet, "/admin/api/rooms/export-room/state", nil)
	reqState.Header.Set("Authorization", "Bearer admin-pass")
	wState := httptest.NewRecorder()
	h.ServeAdminRoomAction(wState, reqState)

	if wState.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wState.Code, wState.Body.String())
	}
	if !strings.Contains(wState.Body.String(), "Collaborative Doc") {
		t.Fatalf("expected state to contain 'Collaborative Doc', got: %s", wState.Body.String())
	}

	// Test GET /admin/api/rooms/export-room/snapshot
	reqSnap := httptest.NewRequest(http.MethodGet, "/admin/api/rooms/export-room/snapshot", nil)
	reqSnap.Header.Set("Authorization", "Bearer admin-pass")
	wSnap := httptest.NewRecorder()
	h.ServeAdminRoomAction(wSnap, reqSnap)

	if wSnap.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wSnap.Code, wSnap.Body.String())
	}
	if !strings.Contains(wSnap.Body.String(), `"snapshot"`) {
		t.Fatalf("expected response to contain 'snapshot', got: %s", wSnap.Body.String())
	}

	// Test GET for non-existent room
	reqMissing := httptest.NewRequest(http.MethodGet, "/admin/api/rooms/non-existent/state", nil)
	reqMissing.Header.Set("Authorization", "Bearer admin-pass")
	wMissing := httptest.NewRecorder()
	h.ServeAdminRoomAction(wMissing, reqMissing)

	if wMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing room, got %d", wMissing.Code)
	}
}
