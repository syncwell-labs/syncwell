package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"syncwell/internal/store"
)

func ageRoomFiles(t *testing.T, dir string, now time.Time) {
	t.Helper()
	metas, err := filepath.Glob(filepath.Join(dir, "*.meta"))
	if err != nil || len(metas) != 1 {
		t.Fatalf("metadata files=%v err=%v", metas, err)
	}
	var m map[string]any
	b, err := os.ReadFile(metas[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["created"] = now.Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	b, _ = json.Marshal(m)
	if err := os.WriteFile(metas[0], b, 0o600); err != nil {
		t.Fatal(err)
	}
	logs, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("log files=%v err=%v", logs, err)
	}
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(logs[0], old, old); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionSweepDryRunAndInjectedClock(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Append("old-room", store.Record{Key: "k", Reg: serverReg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ageRoomFiles(t, dir, now)
	h := NewHub(Config{Store: st, Retention: 24 * time.Hour, RetentionDryRun: true})
	if err := h.SweepRetentionAt(now); err != nil {
		t.Fatal(err)
	}
	if rooms, err := st.RoomMetadata(); err != nil || len(rooms) != 1 {
		t.Fatalf("dry-run metadata=%v err=%v", rooms, err)
	}
	h.cfg.RetentionDryRun = false
	if err := h.SweepRetentionAt(now); err != nil {
		t.Fatal(err)
	}
	if rooms, err := st.RoomMetadata(); err != nil || len(rooms) != 0 {
		t.Fatalf("swept metadata=%v err=%v", rooms, err)
	}
}

func TestRetentionSkipsRoomWithConnectedClient(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Append("busy-room", store.Record{Key: "k", Reg: serverReg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ageRoomFiles(t, dir, now)
	h := NewHub(Config{Store: st, Retention: 24 * time.Hour})
	r, err := h.room("busy-room")
	if err != nil {
		t.Fatal(err)
	}
	cl := &Client{room: r, send: make(chan []byte, 2)}
	r.cmds <- command{kind: "join", client: cl}
	select {
	case <-cl.send:
	case <-time.After(time.Second):
		t.Fatal("client did not join room")
	}
	if err := h.SweepRetentionAt(now); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Load("busy-room"); err != nil || len(got) != 1 {
		t.Fatalf("live room state=%v err=%v", got, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
