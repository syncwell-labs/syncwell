package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"syncwell/internal/crdt"
)

var gapsCause = errors.New("test cause")

func gapsSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func gapsWriteDirFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRollbackDirReplacementRestoresOldDirectory covers the success path of
// rollbackDirReplacement: the freshly installed dst is swapped back out and the
// previous directory (old) is restored in its place.
func TestRollbackDirReplacementRestoresOldDirectory(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "dst")
	old := filepath.Join(parent, "old")
	gapsWriteDirFile(t, dst, "new.txt", "new")
	gapsWriteDirFile(t, old, "old.txt", "old")

	if err := rollbackDirReplacement(dst, old, parent); err != nil {
		t.Fatalf("rollbackDirReplacement: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "old.txt")); err != nil || string(data) != "old" {
		t.Fatalf("dst not restored from old: %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("failed replacement left new file behind: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old directory should be gone after rollback: %v", err)
	}
}

// TestRollbackDirReplacementReportsMissingDestination covers the error branch
// where the replacement directory never existed.
func TestRollbackDirReplacementReportsMissingDestination(t *testing.T) {
	parent := t.TempDir()
	old := filepath.Join(parent, "old")
	gapsWriteDirFile(t, old, "old.txt", "old")
	if err := rollbackDirReplacement(filepath.Join(parent, "absent"), old, parent); err == nil {
		t.Fatal("rollback of a missing dst should fail")
	}
}

// TestRollbackPlainRestoreRestoresSavedFiles covers the success path: installed
// files are removed and saved files are renamed back from oldDir.
func TestRollbackPlainRestoreRestoresSavedFiles(t *testing.T) {
	dst := t.TempDir()
	oldDir := filepath.Join(t.TempDir(), "old")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "installed.log"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "saved.log"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	keepStage := false
	got := rollbackPlainRestore(dst, oldDir, []string{"installed.log"}, []string{"saved.log"}, t.TempDir(), &keepStage, gapsCause)
	if got != gapsCause {
		t.Fatalf("rollbackPlainRestore = %v, want original cause", got)
	}
	if keepStage {
		t.Fatal("clean rollback should not keep the staging directory")
	}
	if _, err := os.Stat(filepath.Join(dst, "installed.log")); !os.IsNotExist(err) {
		t.Fatalf("installed file not removed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "saved.log")); err != nil || string(data) != "old" {
		t.Fatalf("saved file not restored: %q err=%v", data, err)
	}
}

// TestRollbackPlainRestoreKeepsStageWhenRollbackFails covers the branch where
// the destination is gone, so restoring saved files fails and the staging
// directory is kept for manual recovery.
func TestRollbackPlainRestoreKeepsStageWhenRollbackFails(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "absent")
	oldDir := filepath.Join(t.TempDir(), "old")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "saved.log"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	keepStage := false
	got := rollbackPlainRestore(dst, oldDir, nil, []string{"saved.log"}, t.TempDir(), &keepStage, gapsCause)
	if got == nil || got == gapsCause {
		t.Fatalf("expected a wrapped rollback failure, got %v", got)
	}
	if !keepStage {
		t.Fatal("failed rollback should keep the staging directory")
	}
}

// TestReplaceStagedDirRejectsNonDirectoryDestination covers the guard that
// refuses to install over a regular file.
func TestReplaceStagedDirRejectsNonDirectoryDestination(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "file")
	if err := os.WriteFile(dst, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(parent, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := replaceStagedDir(stage, dst, parent, "old-*"); err == nil {
		t.Fatal("replaceStagedDir should reject a file destination")
	}
}

// TestReplaceStagedDirInstallsIntoMissingDestination covers the rename-only
// branch used when the destination does not exist yet.
func TestReplaceStagedDirInstallsIntoMissingDestination(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "dst")
	stage := filepath.Join(parent, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "a.log"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceStagedDir(stage, dst, parent, "old-*"); err != nil {
		t.Fatalf("replaceStagedDir: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "a.log")); err != nil || string(data) != "a" {
		t.Fatalf("staged directory not installed: %q err=%v", data, err)
	}
}

// TestOpenRegularRejectsNonRegularFiles covers directory and symlink refusal
// plus the regular-file success path.
func TestOpenRegularRejectsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := openRegular(dir); err == nil {
		t.Fatal("openRegular should reject a directory")
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openRegular(link); err == nil {
		t.Fatal("openRegular should reject a symlink")
	}
	f, err := openRegular(target)
	if err != nil {
		t.Fatalf("openRegular(regular): %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestCopyVerifiedDetectsChangedSource covers the mismatch branch and the
// successful verified copy.
func TestCopyVerifiedDetectsChangedSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.log")
	payload := []byte("payload")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.log")
	if err := copyVerified(dst, src, BackupFile{Name: "src.log", Size: 1, SHA256: "deadbeef"}); err == nil {
		t.Fatal("copyVerified should reject a size/hash mismatch")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("failed copy left destination behind: %v", statErr)
	}
	if err := copyVerified(dst, src, BackupFile{Name: "src.log", Size: int64(len(payload)), SHA256: gapsSHA256(payload)}); err != nil {
		t.Fatalf("copyVerified valid: %v", err)
	}
}

// TestDirectoriesOverlapAndPathContains covers the symmetry and edge branches
// of the path containment helpers.
func TestDirectoriesOverlapAndPathContains(t *testing.T) {
	base := t.TempDir()
	child := filepath.Join(base, "child")
	sibling := filepath.Join(t.TempDir(), "sibling")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	if !directoriesOverlap(base, base) {
		t.Fatal("a directory should overlap itself")
	}
	if !directoriesOverlap(base, child) || !directoriesOverlap(child, base) {
		t.Fatal("parent/child overlap should be symmetric")
	}
	if directoriesOverlap(base, sibling) {
		t.Fatal("sibling directories should not overlap")
	}
	if !pathContains(base, base) {
		t.Fatal("pathContains(self) should be true")
	}
	if !pathContains(base, child) {
		t.Fatal("pathContains(child) should be true")
	}
	if pathContains(child, base) {
		t.Fatal("pathContains(reverse) should be false")
	}
	if pathContains("relative", "/abs") {
		t.Fatal("unrelated absolute/relative paths should not be contained")
	}
}

// TestBackupRejectsEmptyAndOverlappingDestination covers the early guard rails
// in Backup before any file copying happens.
func TestBackupRejectsEmptyAndOverlappingDestination(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Backup(""); err == nil {
		t.Fatal("empty backup destination should fail")
	}
	if _, err := s.Backup(filepath.Join(dir, "inside")); err == nil {
		t.Fatal("backup destination inside the store should fail")
	}
}

// TestRestoreRejectsEmptyAndOverlappingPaths covers the Restore argument and
// overlap guards using a real backup.
func TestRestoreRejectsEmptyAndOverlappingPaths(t *testing.T) {
	if _, err := Restore("", t.TempDir()); err == nil {
		t.Fatal("empty source should fail")
	}
	if _, err := Restore(t.TempDir(), ""); err == nil {
		t.Fatal("empty destination should fail")
	}
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
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(backup, filepath.Join(backup, "inside")); err == nil {
		t.Fatal("restore destination inside the source should fail")
	}
}

// TestWriteLogRejectsUnwritablePath covers the open-failure branch in writeLog.
func TestWriteLogRejectsUnwritablePath(t *testing.T) {
	state := map[string]crdt.Register{"k": reg("v", 1, "A")}
	if err := writeLog(filepath.Join(t.TempDir(), "missing", "x.log"), state); err == nil {
		t.Fatal("writeLog into a missing directory should fail")
	}
}
