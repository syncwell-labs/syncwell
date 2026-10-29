package store

import (
	"encoding/json"
	"os"
	"testing"

	"syncwell/internal/crdt"
)

func reg(v string, c uint64, n string) crdt.Register {
	b, _ := json.Marshal(v)
	return crdt.Register{Value: b, TS: crdt.Timestamp{Counter: c, Node: n}}
}

func TestAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append("room1", Record{Key: "title", Reg: reg("hello", 1, "A")}); err != nil {
		t.Fatal(err)
	}
	// A later write to the same key, plus a stale one that must lose on load.
	if err := s.Append("room1", Record{Key: "title", Reg: reg("world", 2, "A")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append("room1", Record{Key: "title", Reg: reg("stale", 1, "Z")}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen (simulating a restart) and verify LWW reconstruction.
	s2, _ := Open(dir)
	defer s2.Close()
	snap, err := s2.Load("room1")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	json.Unmarshal(snap["title"].Value, &got)
	if got != "world" {
		t.Fatalf("want world after load, got %q", got)
	}
}

func TestMetadataAndDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append("room/name", Record{Key: "k", Reg: reg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	meta, err := s.RoomMetadata()
	if err != nil || len(meta) != 1 || meta[0].Room != "room/name" {
		t.Fatalf("metadata=%+v err=%v", meta, err)
	}
	path := s.metadataPath("room/name")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("metadata permissions = %o", info.Mode().Perm())
	}
	if err := s.Delete("room/name"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load("room/name"); err != nil || len(got) != 0 {
		t.Fatalf("Load after delete = %v, %v", got, err)
	}
	if err := s.Delete("room/name"); err != nil {
		t.Fatalf("delete should be idempotent: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("metadata remains: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseRoomKeepsLogAndUpdatesPersistenceStats(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Append("room", Record{Key: "k", Reg: reg("v", 1, "n")}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats()
	if err != nil || stats.OpenFiles != 1 || stats.OpenLogs != 1 || stats.Bytes == 0 {
		t.Fatalf("open stats=%+v err=%v", stats, err)
	}
	if err := s.CloseRoom("room"); err != nil {
		t.Fatal(err)
	}
	stats, err = s.Stats()
	if err != nil || stats.OpenFiles != 0 || stats.OpenLogs != 1 || stats.Bytes == 0 {
		t.Fatalf("closed stats=%+v err=%v", stats, err)
	}
	if err := s.Append("room", Record{Key: "next", Reg: reg("v2", 2, "n")}); err != nil {
		t.Fatal(err)
	}
	stats, err = s.Stats()
	if err != nil || stats.OpenFiles != 1 {
		t.Fatalf("reopened stats=%+v err=%v", stats, err)
	}
}

func TestCompactPreservesState(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	defer s.Close()

	for i := 0; i < 50; i++ {
		s.Append("r", Record{Key: "k", Reg: reg("v", uint64(i+1), "A")})
	}
	doc := crdt.NewLWWMap()
	snap, _ := s.Load("r")
	doc.Load(snap)

	if err := s.Compact("r", doc.Snapshot()); err != nil {
		t.Fatal(err)
	}
	// After compaction the file should hold exactly one line per live key.
	b, _ := os.ReadFile(s.path("r"))
	lines := 0
	for _, c := range b {
		if c == '\n' {
			lines++
		}
	}
	if lines != 1 {
		t.Fatalf("want 1 line after compaction, got %d", lines)
	}
	// And the state is unchanged.
	snap2, _ := s.Load("r")
	if snap2["k"].TS.Counter != 50 {
		t.Fatalf("compaction changed state: %+v", snap2["k"].TS)
	}
}

func TestLoadToleratesTornLine(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Append("r", Record{Key: "ok", Reg: reg("good", 1, "A")})
	s.Close()

	// Simulate a crash mid-write: append a partial JSON line with no newline.
	f, _ := os.OpenFile(s.path("r"), os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString(`{"k":"bad","r":{"v":`)
	f.Close()

	s2, _ := Open(dir)
	defer s2.Close()
	snap, err := s2.Load("r")
	if err != nil {
		t.Fatalf("load should tolerate torn line, got %v", err)
	}
	if _, ok := snap["ok"]; !ok {
		t.Fatal("good record lost")
	}
	if _, ok := snap["bad"]; ok {
		t.Fatal("torn record should be skipped")
	}
}

func TestRoomPathsAreCollisionFree(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.path("a/b") == s.path("a_b") {
		t.Fatal("distinct room IDs must never share a persistence path")
	}
	if err := s.Append("a/b", Record{Key: "owner", Reg: reg("slash", 1, "A")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append("a_b", Record{Key: "owner", Reg: reg("underscore", 1, "B")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.Load("a/b")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Load("a_b")
	if err != nil {
		t.Fatal(err)
	}
	if string(first["owner"].Value) != `"slash"` || string(second["owner"].Value) != `"underscore"` {
		t.Fatalf("room data crossed persistence boundary: first=%s second=%s", first["owner"].Value, second["owner"].Value)
	}
}

func TestLoadRejectsMidFileCorruption(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.Append("r", Record{Key: "ok", Reg: reg("good", 1, "A")})
	s.Close()

	// Corruption in the middle of the log — with valid data after it — must
	// fail loudly instead of silently serving a truncated document.
	f, _ := os.OpenFile(s.path("r"), os.O_WRONLY|os.O_APPEND, 0o644)
	f.WriteString("{not json}\n")
	f.WriteString(`{"key":"later","reg":{"v":"x"}}` + "\n")
	f.Close()

	s2, _ := Open(dir)
	defer s2.Close()
	if _, err := s2.Load("r"); err == nil {
		t.Fatal("mid-file corruption was silently ignored")
	}
}

func TestLoadMigratesBase64RoomPath(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	room := "room with spaces/and:colons"
	line, err := json.Marshal(Record{Key: "title", Reg: reg("base64", 1, "A")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.base64Path(room), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(room)
	if err != nil {
		t.Fatal(err)
	}
	if string(snap["title"].Value) != `"base64"` {
		t.Fatalf("base64-layout state was not loaded: %s", snap["title"].Value)
	}
	if _, err := os.Stat(s.path(room)); err != nil {
		t.Fatalf("hashed path was not written during migration: %v", err)
	}
}

func TestLoadMigratesLegacyRoomPath(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithOptions(dir, Options{MigrateLegacyV1: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	room := "legacy-room"
	legacy := s.legacyPath(room)
	line, err := json.Marshal(Record{Key: "title", Reg: reg("migrated", 1, "A")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Load(room)
	if err != nil {
		t.Fatal(err)
	}
	if string(snap["title"].Value) != `"migrated"` {
		t.Fatalf("legacy state was not loaded: %s", snap["title"].Value)
	}
	if _, err := os.Stat(s.path(room)); err != nil {
		t.Fatalf("v2 path was not written: %v", err)
	}
}

func TestLoadRequiresOptInForLegacyRoomPath(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	line, _ := json.Marshal(Record{Key: "title", Reg: reg("legacy", 1, "A")})
	if err := os.WriteFile(s.legacyPath("legacy-room"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("legacy-room"); err == nil {
		t.Fatal("legacy layout was imported without explicit opt-in")
	}
}

func TestLoadRejectsAmbiguousLegacyRoomPath(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// "a/b" and "a_b" both sanitize to "a_b.log" in the v1 layout. Without
	// rejecting the ambiguous room, either could read the other's history.
	line, err := json.Marshal(Record{Key: "title", Reg: reg("secret", 1, "A")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.legacyPath("a/b"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("a/b"); err == nil {
		t.Fatal("ambiguous legacy room was migrated")
	}
}

func TestLoadWithLimitsRejectsOversizedPersistedState(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		key := string(rune('a' + i))
		if err := s.Append("r", Record{Key: key, Reg: reg("value", uint64(i+1), "A")}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	if _, err := s2.LoadWithLimits("r", LoadLimits{MaxEntries: 3}); err == nil {
		t.Fatal("LoadWithLimits accepted more entries than allowed")
	}
	if _, err := s2.LoadWithLimits("r", LoadLimits{MaxBytes: 32}); err == nil {
		t.Fatal("LoadWithLimits accepted more bytes than allowed")
	}
	if _, err := s2.LoadWithLimits("r", LoadLimits{MaxValueBytes: 2}); err == nil {
		t.Fatal("LoadWithLimits accepted an oversized value")
	}
	if _, err := s2.LoadWithLimits("r", LoadLimits{MaxKeyBytes: 0, MaxValueBytes: 1024, MaxEntries: 10, MaxBytes: 1 << 20}); err != nil {
		t.Fatalf("valid limits rejected: %v", err)
	}
}

func TestLoadWithLimitsRejectsImpossibleRegisters(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append("r", Record{Key: "k", Reg: crdt.Register{Value: json.RawMessage(`"x"`), TS: crdt.Timestamp{Counter: 1, Node: ""}}}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, _ := Open(dir)
	defer s2.Close()
	if _, err := s2.LoadWithLimits("r", LoadLimits{MaxNodeBytes: 64}); err == nil {
		t.Fatal("LoadWithLimits accepted a record with an empty node id")
	}
}
