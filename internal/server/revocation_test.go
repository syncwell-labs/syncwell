package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeWSRejectsRevokedJTI(t *testing.T) {
	const secret = "s3cret"
	const room = "roomA"
	const jti = "revoked-123"
	tok := SignAccessToken(secret, room, 2000, []string{CapRead, CapWrite}, jti)

	h := NewHub(Config{
		Secret:      secret,
		RevokedJTIs: map[string]struct{}{jti: struct{}{}},
	})
	req := httptest.NewRequest(http.MethodGet, "/ws?room="+room+"&token="+tok, nil)
	rr := httptest.NewRecorder()

	h.ServeWS(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked token, got %d", rr.Code)
	}
}
