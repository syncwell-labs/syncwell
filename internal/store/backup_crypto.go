package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
)

const encryptedManifestFile = "manifest.json.age"

type encryptedBackupFile struct {
	Name         string `json:"name"`
	PlainSize    int64  `json:"plain_size"`
	PlainSHA256  string `json:"plain_sha256"`
	CipherSize   int64  `json:"cipher_size"`
	CipherSHA256 string `json:"cipher_sha256"`
}

type encryptedBackupManifest struct {
	Format  int                   `json:"format"`
	Created time.Time             `json:"created"`
	Files   []encryptedBackupFile `json:"files"`
}

// BackupEncrypted writes age-encrypted room logs, metadata and manifest.
func (s *FileStore) BackupEncrypted(dst string, recipients []age.Recipient) (*BackupManifest, error) {
	if dst == "" {
		return nil, errors.New("encrypted backup destination is required")
	}
	if len(recipients) == 0 {
		return nil, errors.New("encrypted backup requires at least one age recipient")
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	resolvedStore, err := filepath.EvalSymlinks(s.dir)
	if err != nil {
		return nil, err
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	resolvedDst := filepath.Join(resolvedParent, filepath.Base(filepath.Clean(dst)))
	if directoriesOverlap(resolvedStore, resolvedDst) {
		return nil, errors.New("encrypted backup destination and store directory must not overlap")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for room, w := range s.bufs {
		if err := w.Flush(); err != nil {
			return nil, err
		}
		if f := s.files[room]; f != nil {
			if err := f.Sync(); err != nil {
				return nil, err
			}
		}
		delete(s.dirty, room)
	}
	stage, err := os.MkdirTemp(parent, ".syncwell-backup-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	manifest := encryptedBackupManifest{Format: 2, Created: time.Now().UTC()}
	plainManifest := &BackupManifest{Format: 2, Created: manifest.Created}
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".log") && !strings.HasSuffix(entry.Name(), ".meta")) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing symlink in backup source: %s", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("refusing non-regular file in backup source: %s", entry.Name())
		}
		item, err := encryptBackupFile(stage, filepath.Join(s.dir, entry.Name()), entry.Name(), info, recipients)
		if err != nil {
			return nil, err
		}
		manifest.Files = append(manifest.Files, item)
		plainManifest.Files = append(plainManifest.Files, BackupFile{Name: item.Name, Size: item.PlainSize, SHA256: item.PlainSHA256})
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(stage, encryptedManifestFile+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	enc, err := age.Encrypt(f, recipients...)
	if err == nil {
		_, err = enc.Write(data)
		if closeErr := enc.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(stage, encryptedManifestFile)); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := syncDir(stage); err != nil {
		return nil, err
	}
	if err := replaceStagedDir(stage, dst, parent, ".syncwell-backup-old-*"); err != nil {
		return nil, err
	}
	return plainManifest, nil
}

func encryptBackupFile(dst, src, name string, expected os.FileInfo, recipients []age.Recipient) (encryptedBackupFile, error) {
	in, err := os.Open(src)
	if err != nil {
		return encryptedBackupFile{}, err
	}
	defer in.Close()
	actual, err := in.Stat()
	if err != nil {
		return encryptedBackupFile{}, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return encryptedBackupFile{}, fmt.Errorf("backup source changed while opening: %s", name)
	}
	target := name + ".age"
	tmp := filepath.Join(dst, target+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return encryptedBackupFile{}, err
	}
	h := sha256.New()
	cw := &countHashWriter{w: io.MultiWriter(f, h)}
	enc, err := age.Encrypt(cw, recipients...)
	plainHash := sha256.New()
	var plainCounter *countHashWriter
	if err == nil {
		plainCounter = &countHashWriter{w: io.MultiWriter(enc, plainHash)}
		_, err = io.Copy(plainCounter, in)
		if closeErr := enc.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return encryptedBackupFile{}, err
	}
	if err := os.Rename(tmp, filepath.Join(dst, target)); err != nil {
		_ = os.Remove(tmp)
		return encryptedBackupFile{}, err
	}
	return encryptedBackupFile{Name: target, PlainSize: plainCounter.n, PlainSHA256: hex.EncodeToString(plainHash.Sum(nil)), CipherSize: cw.n, CipherSHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

type countHashWriter struct {
	w io.Writer
	n int64
}

func (w *countHashWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

// VerifyEncryptedBackup authenticates the manifest and each payload without
// writing any plaintext to disk.
func VerifyEncryptedBackup(dir string, identities []age.Identity) (*BackupManifest, error) {
	if len(identities) == 0 {
		return nil, errors.New("encrypted backup verification requires -restore-identity")
	}
	f, err := openRegular(filepath.Join(dir, encryptedManifestFile))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := age.Decrypt(f, identities...)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(r, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, errors.New("encrypted backup manifest exceeds 16 MiB")
	}
	var m encryptedBackupManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid encrypted backup manifest: %w", err)
	}
	if m.Format != 2 {
		return nil, fmt.Errorf("unsupported encrypted backup format %d", m.Format)
	}
	plain := &BackupManifest{Format: 2, Created: m.Created}
	seen := make(map[string]struct{}, len(m.Files))
	for _, item := range m.Files {
		if item.Name == "" || filepath.Base(item.Name) != item.Name ||
			(!strings.HasSuffix(item.Name, ".log.age") && !strings.HasSuffix(item.Name, ".meta.age")) {
			return nil, fmt.Errorf("unsafe encrypted backup entry %q", item.Name)
		}
		if _, ok := seen[item.Name]; ok {
			return nil, fmt.Errorf("duplicate encrypted backup entry %q", item.Name)
		}
		seen[item.Name] = struct{}{}
		path := filepath.Join(dir, item.Name)
		in, err := openRegular(path)
		if err != nil {
			return nil, err
		}
		cipherHash := sha256.New()
		cn, err := io.Copy(cipherHash, in)
		if err != nil {
			in.Close()
			return nil, err
		}
		ch := hex.EncodeToString(cipherHash.Sum(nil))
		if cn != item.CipherSize || ch != item.CipherSHA256 {
			in.Close()
			return nil, fmt.Errorf("backup file %s: ciphertext checksum mismatch", item.Name)
		}
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			in.Close()
			return nil, err
		}
		dec, err := age.Decrypt(in, identities...)
		if err != nil {
			in.Close()
			return nil, err
		}
		h := sha256.New()
		n, err := io.Copy(h, dec)
		in.Close()
		if err != nil {
			return nil, fmt.Errorf("backup file %s: decrypt: %w", item.Name, err)
		}
		if n != item.PlainSize || hex.EncodeToString(h.Sum(nil)) != item.PlainSHA256 {
			return nil, fmt.Errorf("backup file %s: plaintext checksum mismatch", item.Name)
		}
		name := strings.TrimSuffix(item.Name, ".age")
		plain.Files = append(plain.Files, BackupFile{Name: name, Size: n, SHA256: item.PlainSHA256})
	}
	return plain, nil
}

// RestoreEncrypted verifies the complete backup before replacing destination logs.
func RestoreEncrypted(src, dst string, identities []age.Identity) (*BackupManifest, error) {
	if src == "" || dst == "" {
		return nil, errors.New("encrypted backup source and restore destination are required")
	}
	m, err := VerifyEncryptedBackup(src, identities)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	resolvedSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		return nil, err
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, err
	}
	resolvedDst := filepath.Join(resolvedParent, filepath.Base(filepath.Clean(dst)))
	if directoriesOverlap(resolvedSrc, resolvedDst) {
		return nil, errors.New("encrypted backup source and restore destination must not overlap")
	}
	stage, err := os.MkdirTemp(parent, "syncwell-restore-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	for _, bf := range m.Files {
		in, err := os.Open(filepath.Join(src, bf.Name+".age"))
		if err != nil {
			return nil, err
		}
		dec, err := age.Decrypt(in, identities...)
		if err != nil {
			in.Close()
			return nil, err
		}
		outPath := filepath.Join(stage, bf.Name)
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			in.Close()
			return nil, err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, h), dec)
		if copyErr == nil && (n != bf.Size || hex.EncodeToString(h.Sum(nil)) != bf.SHA256) {
			copyErr = fmt.Errorf("backup file %s changed during restore", bf.Name)
		}
		var syncErr error
		if copyErr == nil {
			syncErr = out.Sync()
		}
		closeErr := out.Close()
		in.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if syncErr != nil {
			return nil, syncErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if err := syncDir(stage); err != nil {
		return nil, err
	}
	if err := replaceStagedDir(stage, dst, parent, "syncwell-restore-old-*"); err != nil {
		return nil, err
	}
	return m, nil
}

// replaceStagedDir installs a fully written directory and retains the previous
// destination until the rename is durable in the parent directory.
func replaceStagedDir(stage, dst, parent, oldPattern string) error {
	info, err := os.Lstat(dst)
	if os.IsNotExist(err) {
		if err := os.Rename(stage, dst); err != nil {
			return err
		}
		if err := syncDir(parent); err != nil {
			rollbackErr := os.Rename(dst, stage)
			if rollbackErr == nil {
				rollbackErr = syncDir(parent)
			}
			return errors.Join(err, rollbackErr)
		}
		return nil
	} else if err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("backup/restore destination is not a directory: %s", dst)
	}

	old, err := os.MkdirTemp(parent, oldPattern)
	if err != nil {
		return err
	}
	if err := os.Remove(old); err != nil {
		return err
	}
	if err := os.Rename(dst, old); err != nil {
		return err
	}
	if err := os.Rename(stage, dst); err != nil {
		rollbackErr := os.Rename(old, dst)
		if rollbackErr == nil {
			rollbackErr = syncDir(parent)
		}
		return errors.Join(err, rollbackErr)
	}
	if err := syncDir(parent); err != nil {
		rollbackErr := rollbackDirReplacement(dst, old, parent)
		return errors.Join(err, rollbackErr)
	}
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	return syncDir(parent)
}

func rollbackDirReplacement(dst, old, parent string) error {
	failed, err := os.MkdirTemp(parent, ".syncwell-replacement-failed-*")
	if err != nil {
		return err
	}
	if err := os.Remove(failed); err != nil {
		return err
	}
	if err := os.Rename(dst, failed); err != nil {
		return err
	}
	if err := os.Rename(old, dst); err != nil {
		restoreNewErr := os.Rename(failed, dst)
		if restoreNewErr == nil {
			restoreNewErr = syncDir(parent)
		}
		return errors.Join(err, restoreNewErr)
	}
	syncErr := syncDir(parent)
	removeErr := os.RemoveAll(failed)
	cleanupSyncErr := syncDir(parent)
	return errors.Join(syncErr, removeErr, cleanupSyncErr)
}

func directoriesOverlap(a, b string) bool {
	a, errA := filepath.Abs(filepath.Clean(a))
	b, errB := filepath.Abs(filepath.Clean(b))
	if errA != nil || errB != nil {
		return true
	}
	return pathContains(a, b) || pathContains(b, a)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
