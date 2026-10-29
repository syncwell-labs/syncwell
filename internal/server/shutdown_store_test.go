package server

import (
	"context"
	"testing"
	"time"

	"syncwell/internal/store"
)

func TestRoomEvictionClosesAppendDescriptor(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Append("room", store.Record{Key: "k", Reg: serverReg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	h := NewHub(Config{Store: st, IdleRoomTTL: -1})
	if _, err := h.room("room"); err != nil {
		t.Fatal(err)
	}
	if !h.EvictRoom("room") {
		t.Fatal("room eviction failed")
	}
	stats, err := st.Stats()
	if err != nil || stats.OpenFiles != 0 || stats.OpenLogs != 1 {
		t.Fatalf("persistence after eviction=%+v err=%v", stats, err)
	}
}

func TestHubShutdownCompactsAndClosesRoomStore(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Append("room", store.Record{Key: "k", Reg: serverReg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	h := NewHub(Config{Store: st, IdleRoomTTL: -1})
	if _, err := h.room("room"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := st.Stats()
	if err != nil || stats.OpenFiles != 0 || stats.OpenLogs != 1 {
		t.Fatalf("persistence after shutdown=%+v err=%v", stats, err)
	}
	loaded, err := st.Load("room")
	if err != nil || len(loaded) != 1 {
		t.Fatalf("shutdown persisted state=%v err=%v", loaded, err)
	}
}
