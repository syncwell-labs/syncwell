package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"syncwell/internal/crdt"
)

func TestParseFsyncMode(t *testing.T) {
	cases := map[string]FsyncMode{
		"":       FsyncBatch,
		"batch":  FsyncBatch,
		"BATCH":  FsyncBatch,
		" os ":   FsyncOS,
		"none":   FsyncOS,
		"every":  FsyncEvery,
		"always": FsyncEvery,
	}
	for in, want := range cases {
		got, err := ParseFsyncMode(in)
		if err != nil {
			t.Fatalf("ParseFsyncMode(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("ParseFsyncMode(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseFsyncMode("sometimes"); err == nil {
		t.Fatal("invalid mode was accepted")
	}
}

func TestAppendFlushesWithoutClose(t *testing.T) {
	for _, mode := range []FsyncMode{FsyncOS, FsyncBatch, FsyncEvery} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := t.TempDir()
			s, err := OpenWithOptions(dir, Options{Fsync: mode})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.Append("r", Record{Key: "k", Reg: reg("v", 1, "A")}); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(s.path("r"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), `"k"`) {
				t.Fatalf("record was still buffered after Append (mode %s)", mode)
			}
		})
	}
}

func TestLogSizeTracksFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := s.LogSize("r"); got != 0 {
		t.Fatalf("fresh room size = %d, want 0", got)
	}
	for i := 0; i < 5; i++ {
		if err := s.Append("r", Record{Key: "k", Reg: reg("v", uint64(i+1), "A")}); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(s.path("r"))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.LogSize("r"); got != fi.Size() {
		t.Fatalf("LogSize = %d, file size = %d", got, fi.Size())
	}

	doc := crdt.NewLWWMap()
	snap, _ := s.Load("r")
	doc.Load(snap)
	if err := s.Compact("r", doc.Snapshot()); err != nil {
		t.Fatal(err)
	}
	fi, err = os.Stat(s.path("r"))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.LogSize("r"); got != fi.Size() {
		t.Fatalf("LogSize after compact = %d, file size = %d", got, fi.Size())
	}
}

// TestLoadAtEveryTruncationPoint simulates a crash at every possible byte of
// the log. Truncation can only ever leave a torn final line, which Load must
// tolerate while returning the intact prefix.
func TestLoadAtEveryTruncationPoint(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"k0", "k1", "k2", "k3", "k4"}
	for i, k := range keys {
		if err := s.Append("r", Record{Key: k, Reg: reg(k, uint64(i+1), "A")}); err != nil {
			t.Fatal(err)
		}
	}
	name := filepath.Base(s.path("r"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}

	for cut := 0; cut <= len(full); cut++ {
		cutDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(cutDir, name), full[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(cutDir)
		if err != nil {
			t.Fatal(err)
		}
		snap, err := s.Load("r")
		s.Close()
		if err != nil {
			t.Fatalf("cut %d: load failed on torn tail: %v", cut, err)
		}
		// A record is intact if its line is valid JSON; the final newline is
		// only a delimiter, so a cut exactly after "}" still counts.
		complete := 0
		parts := strings.Split(string(full[:cut]), "\n")
		for i, p := range parts {
			if p == "" {
				continue
			}
			var rec Record
			if json.Unmarshal([]byte(p), &rec) == nil {
				complete++
			} else if i != len(parts)-1 {
				t.Fatalf("cut %d: mid-file corruption in prefix", cut)
			}
		}
		if len(snap) != complete {
			t.Fatalf("cut %d: got %d registers, want %d", cut, len(snap), complete)
		}
		for i, k := range keys {
			_, ok := snap[k]
			if ok != (i < complete) {
				t.Fatalf("cut %d: key %s present=%v, want %v", cut, k, ok, i < complete)
			}
		}
	}
}

func TestBackupRestore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Append("room", Record{Key: "k", Reg: reg("v", uint64(i+1), "A")}); err != nil {
			t.Fatal(err)
		}
	}

	backup := t.TempDir()
	m, err := s.Backup(backup)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 1 {
		t.Fatalf("manifest lists %d files, want 1", len(m.Files))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A backup taken while the server was running must be complete on its own.
	if _, err := VerifyBackup(backup); err != nil {
		t.Fatalf("verify: %v", err)
	}

	restored := t.TempDir()
	stale := filepath.Join(restored, "deadbeef.log")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(backup, restored); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale log survived restore: %v", err)
	}
	s2, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	snap, err := s2.Load("room")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	json.Unmarshal(snap["k"].Value, &got)
	if got != "v" || snap["k"].TS.Counter != 3 {
		t.Fatalf("restored state = %s @ %d, want v @ 3", got, snap["k"].TS.Counter)
	}
}

func TestVerifyBackupDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append("room", Record{Key: "k", Reg: reg("v", 1, "A")}); err != nil {
		t.Fatal(err)
	}
	backup := t.TempDir()
	if _, err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	s.Close()

	logPath := filepath.Join(backup, "MANIFEST.json")
	m, err := VerifyBackup(backup)
	if err != nil {
		t.Fatal(err)
	}

	// Flip one byte of the copied log.
	roomLog := filepath.Join(backup, m.Files[0].Name)
	b, err := os.ReadFile(roomLog)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 0xff
	if err := os.WriteFile(roomLog, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(backup); err == nil {
		t.Fatal("checksum mismatch was not detected")
	}

	// Restore must refuse a corrupt backup rather than copying it over live data.
	if _, err := Restore(backup, t.TempDir()); err == nil {
		t.Fatal("restore accepted a corrupt backup")
	}

	// A manifest entry that escapes the backup directory is rejected.
	traversal := BackupManifest{Format: 1, Files: []BackupFile{{Name: "../evil.log"}}}
	data, _ := json.Marshal(traversal)
	if err := os.WriteFile(logPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(backup); err == nil {
		t.Fatal("path traversal in manifest was not rejected")
	}
}

func TestVerifyBackupRejectsBadManifests(t *testing.T) {
	goodManifest := func() BackupManifest {
		return BackupManifest{Format: 1, Files: []BackupFile{{Name: "x.log", Size: 1, SHA256: "00"}}}
	}
	cases := []struct {
		name    string
		prepare func(dir string) error
	}{
		{"missing manifest", func(dir string) error { return nil }},
		{"invalid json", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, ManifestFile), []byte("{"), 0o600)
		}},
		{"unsupported format", func(dir string) error {
			data, _ := json.Marshal(BackupManifest{Format: 99})
			return os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o600)
		}},
		{"missing file", func(dir string) error {
			data, _ := json.Marshal(goodManifest())
			return os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o600)
		}},
		{"size mismatch", func(dir string) error {
			m := goodManifest()
			m.Files[0].Size = 2
			data, _ := json.Marshal(m)
			if err := os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o600); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "x.log"), []byte("x"), 0o600)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := tc.prepare(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyBackup(dir); err == nil {
				t.Fatal("expected verify error")
			}
		})
	}
}

func TestBackupAndRestoreRejectBadDestinations(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append("room", Record{Key: "k", Reg: reg("v", 1, "A")}); err != nil {
		t.Fatal(err)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(blocker); err == nil {
		t.Fatal("backup into a file path should fail")
	}

	backup := t.TempDir()
	if _, err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(backup, blocker); err == nil {
		t.Fatal("restore into a file path should fail")
	}
}
