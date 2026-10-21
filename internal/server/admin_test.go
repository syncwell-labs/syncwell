package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
