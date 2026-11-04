package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syncwell/internal/store"
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

func TestServeMetricsExposesPersistenceInventory(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Append("room", store.Record{Key: "k", Reg: serverReg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	h := NewHub(Config{Store: st, AdminToken: "admin"})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer admin")
	rr := httptest.NewRecorder()
	h.ServeMetrics(rr, req)
	for _, want := range []string{"syncwell_open_log_files 1", "syncwell_open_log_bytes "} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, rr.Body.String())
		}
	}
}

func TestServeMetricsExposesRoomCounters(t *testing.T) {
	h := NewHub(Config{AdminToken: "secret-admin"})
	h.redisLeasesCurrent.Store(1)
	h.redisLeaseAcquired.Add(1)
	h.redisRehomes.Add(1)
	h.redisLeaseLosses.Add(1)
	h.redisStreamErrors.Add(1)
	h.metrics.redisReplay.observe(0.25)
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
		"syncwell_rooms_low_water_stuck 0",
		"syncwell_process_open_fds ",
		"syncwell_process_fd_limit ",
		"syncwell_goroutines ",
		"syncwell_heap_bytes ",
		"syncwell_apply_duration_seconds_count 0",
		"syncwell_apply_duration_seconds_bucket{le=\"+Inf\"} 0",
		"syncwell_inbound_frame_bytes_count 0",
		"syncwell_outbound_frame_bytes_count 0",
		"syncwell_redis_leases_current 1",
		"syncwell_redis_lease_acquired_total 1",
		"syncwell_redis_rehomes_total 1",
		"syncwell_redis_lease_losses_total 1",
		"syncwell_redis_stream_errors_total 1",
		"syncwell_redis_replay_duration_seconds_count 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "{room=") {
		t.Fatal("metrics must not expose room-id labels")
	}
}
