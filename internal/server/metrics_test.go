package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeMetricsRequiresAdminToken(t *testing.T) {
	h := NewHub(Config{AdminToken: "secret-admin"})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	h.ServeMetrics(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestServeMetricsExposesRoomCounters(t *testing.T) {
	h := NewHub(Config{AdminToken: "secret-admin"})
	if _, err := h.room("roomA"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-admin")
	rr := httptest.NewRecorder()

	h.ServeMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"syncwell_rooms 1",
		"syncwell_clients_total 0",
		"syncwell_room_clients{room=\"roomA\"} 0",
		"syncwell_room_low_water{room=\"roomA\"} 0",
		"syncwell_apply_duration_seconds_count 0",
		"syncwell_apply_duration_seconds_bucket{le=\"+Inf\"} 0",
		"syncwell_inbound_frame_bytes_count 0",
		"syncwell_outbound_frame_bytes_count 0",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, body)
		}
	}
}
