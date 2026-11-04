package server

import (
	"bytes"
	"context"
	"log"
	"strings"
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

func TestHubShutdownLogsIncompleteRoomCountsForEveryDrainWait(t *testing.T) {
	for _, mode := range []string{"anti-entropy", "queue-admission", "room-completion"} {
		t.Run(mode, func(t *testing.T) {
			h := NewHub(Config{IdleRoomTTL: -1})
			r := &Room{cmds: make(chan command, 1), done: make(chan struct{})}
			h.rooms["stalled"] = r
			switch mode {
			case "anti-entropy":
				h.aeStop = make(chan struct{})
				h.aeDone = make(chan struct{})
			case "queue-admission":
				r.cmds <- command{kind: "occupy-queue"}
			}

			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if err := h.Shutdown(ctx); err != context.DeadlineExceeded {
				t.Fatalf("shutdown error = %v, want deadline exceeded", err)
			}
			line := output.String()
			for _, field := range []string{
				`"event":"shutdown_incomplete"`,
				`"rooms_total":1`,
				`"rooms_completed":0`,
				`"rooms_remaining":1`,
			} {
				if !strings.Contains(line, field) {
					t.Errorf("shutdown log missing %s: %s", field, line)
				}
			}
		})
	}
}
