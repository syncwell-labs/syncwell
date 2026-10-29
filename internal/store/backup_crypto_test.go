package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"syncwell/internal/crdt"
)

func TestEncryptedBackupRoundTripAndTamper(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	marker := "plaintext-room-secret-marker"
	if err := s.Append("secret-room", Record{Key: "k", Reg: crdt.Register{Value: []byte(`"` + marker + `"`), TS: crdt.Timestamp{Counter: 1, Node: "n"}}}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err := s.BackupEncrypted(backup, nil); err == nil {
		t.Fatal("backup without a recipient must fail")
	}
	if _, err := s.BackupEncrypted(backup, []age.Recipient{identity.Recipient()}); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyEncryptedBackup(backup, []age.Identity{identity})
	if err != nil || len(verified.Files) != 2 {
		t.Fatalf("verify=%+v err=%v", verified, err)
	}
	entries, err := os.ReadDir(backup)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(backup, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), marker) {
			t.Fatalf("plaintext marker found in %s", e.Name())
		}
	}
	if _, err := VerifyEncryptedBackup(backup, []age.Identity{other}); err == nil {
		t.Fatal("wrong identity unexpectedly verified backup")
	}
	protected := filepath.Join(t.TempDir(), "protected")
	if err := os.MkdirAll(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	markerFile := filepath.Join(protected, "keep.txt")
	if err := os.WriteFile(markerFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreEncrypted(backup, protected, []age.Identity{other}); err == nil {
		t.Fatal("wrong identity unexpectedly restored backup")
	}
	if data, err := os.ReadFile(markerFile); err != nil || string(data) != "keep" {
		t.Fatalf("failed restore changed existing directory: %q err=%v", data, err)
	}
	dst := filepath.Join(t.TempDir(), "restored")
	if _, err := RestoreEncrypted(backup, dst, []age.Identity{identity}); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	state, err := restored.Load("secret-room")
	if err != nil || len(state) != 1 || !strings.Contains(string(state["k"].Value), marker) {
		t.Fatalf("restored state=%v err=%v", state, err)
	}
	// A valid replacement of an existing destination swaps the staged directory atomically.
	if err := os.WriteFile(filepath.Join(dst, "stale.log"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreEncrypted(backup, dst, []age.Identity{identity}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale.log")); !os.IsNotExist(err) {
		t.Fatalf("stale destination file remains: %v", err)
	}
	if _, err := s.BackupEncrypted(backup, []age.Recipient{identity.Recipient()}); err != nil {
		t.Fatalf("replacing prior backup failed: %v", err)
	}
	// A modified ciphertext and a truncated payload must both fail authentication.
	logFile := filepath.Join(backup, strings.Repeat("0", 64)+".log.age")
	files, _ := filepath.Glob(filepath.Join(backup, "*.log.age"))
	if len(files) != 1 {
		t.Fatalf("encrypted logs=%v", files)
	}
	logFile = files[0]
	original, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), original...), 0), append([]byte(nil), original[:len(original)-1]...)} {
		if err := os.WriteFile(logFile, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyEncryptedBackup(backup, []age.Identity{identity}); err == nil {
			t.Fatal("tampered/truncated ciphertext verified")
		}
	}
	if err := os.WriteFile(logFile, original, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(backup, encryptedManifestFile)
	mb, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mb[len(mb)-1] ^= 1
	if err := os.WriteFile(manifest, mb, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyEncryptedBackup(backup, []age.Identity{identity}); err == nil {
		t.Fatal("tampered encrypted manifest verified")
	}
}
