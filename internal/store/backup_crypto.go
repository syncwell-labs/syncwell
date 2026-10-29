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
	if len(recipients) == 0 {
		return nil, errors.New("encrypted backup requires at least one age recipient")
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
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
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
		item, err := encryptBackupFile(stage, filepath.Join(s.dir, entry.Name()), entry.Name(), recipients)
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
	if _, err := os.Stat(dst); err == nil {
		old, err := os.MkdirTemp(parent, ".syncwell-backup-old-*")
		if err != nil {
			return nil, err
		}
		if err := os.Remove(old); err != nil {
			return nil, err
		}
		if err := os.Rename(dst, old); err != nil {
			return nil, err
		}
		if err := os.Rename(stage, dst); err != nil {
			_ = os.Rename(old, dst)
			return nil, err
		}
		if err := os.RemoveAll(old); err != nil {
			return nil, err
		}
	} else if os.IsNotExist(err) {
		if err := os.Rename(stage, dst); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if err := syncDir(parent); err != nil {
		return nil, err
	}
	return plainManifest, nil
}

func encryptBackupFile(dst, src, name string, recipients []age.Recipient) (encryptedBackupFile, error) {
	in, err := os.Open(src)
	if err != nil {
		return encryptedBackupFile{}, err
	}
	defer in.Close()
	target := name + ".age"
	tmp := filepath.Join(dst, target+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return encryptedBackupFile{}, err
	}
	h := sha256.New()
	cw := &countHashWriter{w: io.MultiWriter(f, h)}
	enc, err := age.Encrypt(cw, recipients...)
	if err == nil {
		_, err = io.Copy(enc, in)
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
	plainHash, plainSize, err := hashFile(src)
	if err != nil {
		return encryptedBackupFile{}, err
	}
	return encryptedBackupFile{Name: target, PlainSize: plainSize, PlainSHA256: plainHash, CipherSize: cw.n, CipherSHA256: hex.EncodeToString(h.Sum(nil))}, nil
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

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// VerifyEncryptedBackup authenticates the manifest and each payload without
// writing any plaintext to disk.
func VerifyEncryptedBackup(dir string, identities []age.Identity) (*BackupManifest, error) {
	if len(identities) == 0 {
		return nil, errors.New("encrypted backup verification requires -restore-identity")
	}
	f, err := os.Open(filepath.Join(dir, encryptedManifestFile))
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
	for _, item := range m.Files {
		if item.Name == "" || filepath.Base(item.Name) != item.Name || !strings.HasSuffix(item.Name, ".age") {
			return nil, fmt.Errorf("unsafe encrypted backup entry %q", item.Name)
		}
		path := filepath.Join(dir, item.Name)
		ch, cn, err := hashFile(path)
		if err != nil {
			return nil, err
		}
		if cn != item.CipherSize || ch != item.CipherSHA256 {
			return nil, fmt.Errorf("backup file %s: ciphertext checksum mismatch", item.Name)
		}
		in, err := os.Open(path)
		if err != nil {
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
	m, err := VerifyEncryptedBackup(src, identities)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dst), "syncwell-restore-*")
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
		_, copyErr := io.Copy(out, dec)
		syncErr := out.Sync()
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
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dst); err == nil {
		rollback, err := os.MkdirTemp(parent, "syncwell-restore-old-*")
		if err != nil {
			return nil, err
		}
		if err := os.Remove(rollback); err != nil {
			return nil, err
		}
		if err := os.Rename(dst, rollback); err != nil {
			return nil, err
		}
		if err := os.Rename(stage, dst); err != nil {
			_ = os.Rename(rollback, dst)
			return nil, err
		}
		if err := os.RemoveAll(rollback); err != nil {
			return nil, err
		}
	} else if os.IsNotExist(err) {
		if err := os.Rename(stage, dst); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	if err := syncDir(parent); err != nil {
		return nil, err
	}
	return m, nil
}
