package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"syncwell/internal/store"
	"testing"
	"time"
)

func TestAdminUIAndRoomEndpoints(t *testing.T) {
	h := NewHub(Config{AdminToken: "secret-admin"})
	if _, err := h.room("roomA"); err != nil {
		t.Fatal(err)
	}

	ui := httptest.NewRecorder()
	h.ServeAdminUI(ui, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if ui.Code != http.StatusOK || !strings.Contains(ui.Body.String(), "Syncwell admin") {
		t.Fatalf("admin UI not served: %d", ui.Code)
	}

	unauth := httptest.NewRecorder()
	h.ServeAdminRooms(unauth, httptest.NewRequest(http.MethodGet, "/admin/api/rooms", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauth.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/api/rooms", nil)
	req.Header.Set("Authorization", "Bearer secret-admin")
	rr := httptest.NewRecorder()
	h.ServeAdminRooms(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"roomA"`) {
		t.Fatalf("rooms endpoint missing room: %d %s", rr.Code, rr.Body.String())
	}

	if !h.ResnapshotRoom("roomA") {
		t.Fatal("resnapshot failed for live room")
	}

	evict := httptest.NewRequest(http.MethodPost, "/admin/api/rooms/roomA/evict", nil)
	evict.Header.Set("Authorization", "Bearer secret-admin")
	er := httptest.NewRecorder()
	h.ServeAdminRoomAction(er, evict)
	if er.Code != http.StatusOK {
		t.Fatalf("evict failed: %d %s", er.Code, er.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for h.lookupRoom("roomA") != nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.lookupRoom("roomA") != nil {
		t.Fatal("room still present after eviction")
	}
	missing := httptest.NewRequest(http.MethodPost, "/admin/api/rooms/roomA/resnapshot", nil)
	missing.Header.Set("Authorization", "Bearer secret-admin")
	mr := httptest.NewRecorder()
	h.ServeAdminRoomAction(mr, missing)
	if mr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing room, got %d", mr.Code)
	}
}

func TestAdminPurgeDeletesPersistentRoom(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Append("purge-me", store.Record{Key: "k", Reg: serverReg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	h := NewHub(Config{Store: st, AdminToken: "secret-admin"})
	if _, err := h.room("purge-me"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/rooms/purge-me/purge", nil)
	req.Header.Set("Authorization", "Bearer secret-admin")
	response := httptest.NewRecorder()
	h.ServeAdminRoomAction(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", response.Code, response.Body.String())
	}
	if got, err := st.Load("purge-me"); err != nil || len(got) != 0 {
		t.Fatalf("state after purge=%v err=%v", got, err)
	}
	if h.lookupRoom("purge-me") != nil {
		t.Fatal("purged room remains live")
	}
}
