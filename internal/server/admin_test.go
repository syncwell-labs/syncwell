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
