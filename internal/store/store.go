// Package store persists room documents so a server survives restarts. The
// implementation is intentionally dependency-light — an append-only op-log file
// per room plus periodic snapshot compaction — to preserve the single-binary,
// no-external-database story. Each log line is one CRDT register; replaying the
// log in any order reconstructs the document, because the merge is a CRDT join.
package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"syncwell/internal/crdt"
)

// Record is one persisted register write (a single append-only log entry).
type Record struct {
	Key string        `json:"k"`
	Reg crdt.Register `json:"r"`
}

// Store persists and restores room documents.
type Store interface {
	// Load returns the reconstructed register state for a room (empty if new).
	Load(room string) (map[string]crdt.Register, error)
	// Append durably records a single winning register write.
	Append(room string, rec Record) error
	// Compact rewrites a room's log to one line per current register, bounding
	// file growth. Atomic: writes a temp file then renames.
	Compact(room string, state map[string]crdt.Register) error
	// Close flushes and closes all open files.
	Close() error
}

// FileStore is the default on-disk Store.
type FileStore struct {
	dir   string
	mu    sync.Mutex
	files map[string]*os.File
	bufs  map[string]*bufio.Writer
}

// Open creates (if needed) the data directory and returns a FileStore.
func Open(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &FileStore{
		dir:   dir,
		files: map[string]*os.File{},
		bufs:  map[string]*bufio.Writer{},
	}, nil
}

// legacyUnsafe is retained only to migrate existing v1 data files. Replacing
// arbitrary characters with '_' was path-safe but not injective: "a/b" and
// "a_b" addressed the same file.
var legacyUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// path maps every room ID to a unique, path-safe, fixed-length filename. A
// SHA-256 hex digest is injective in practice and cannot exceed NAME_MAX even
// for the largest accepted room id (base64 expanded ~4/3 and could overflow).
func (s *FileStore) path(room string) string {
	sum := sha256.Sum256([]byte(room))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".log")
}

// base64Path is the previous (v2) layout, retained for one-way migration.
func (s *FileStore) base64Path(room string) string {
	return filepath.Join(s.dir, base64.RawURLEncoding.EncodeToString([]byte(room))+".log")
}

func (s *FileStore) legacyPath(room string) string {
	return filepath.Join(s.dir, legacyUnsafe.ReplaceAllString(room, "_")+".log")
}

// Load replays the op-log through a fresh CRDT and returns its snapshot. A torn
// final line (from a crash mid-write) is skipped rather than failing the load.
func (s *FileStore) Load(room string) (map[string]crdt.Register, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap, found, err := s.loadPath(s.path(room))
	if err != nil || found {
		return snap, err
	}

	// One-way migration from the two earlier layouts. The base64 name was
	// injective but could exceed NAME_MAX; the legacy sanitised name was not
	// injective. Compact into the hashed layout before returning so future
	// writes never touch the old file.
	for _, alt := range []string{s.base64Path(room), s.legacyPath(room)} {
		if alt == s.path(room) {
			continue
		}
		if _, statErr := os.Stat(alt); statErr != nil {
			continue
		}
		snap, found, err = s.loadPath(alt)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if err := s.compactLocked(room, snap); err != nil {
			return nil, err
		}
		return snap, nil
	}
	return map[string]crdt.Register{}, nil
}

func (s *FileStore) loadPath(path string) (map[string]crdt.Register, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]crdt.Register{}, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	doc := crdt.NewLWWMap()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			// Tolerate only a torn final line (kill -9 mid-write). A malformed
			// record followed by more data means real corruption — failing
			// loudly beats silently serving a truncated history.
			if sc.Scan() {
				return nil, true, fmt.Errorf("corrupt op-log %s: %w", path, err)
			}
			break
		}
		doc.ApplyRegister(rec.Key, rec.Reg)
	}
	if err := sc.Err(); err != nil {
		return nil, true, err
	}
	return doc.Snapshot(), true, nil
}

func (s *FileStore) writer(room string) (*bufio.Writer, error) {
	if w, ok := s.bufs[room]; ok {
		return w, nil
	}
	f, err := os.OpenFile(s.path(room), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	w := bufio.NewWriter(f)
	s.files[room] = f
	s.bufs[room] = w
	return w, nil
}

// Append writes one record and flushes it to the OS (survives process kill;
// fsync for power-loss durability is a future knob, see DESIGN.md).
func (s *FileStore) Append(room string, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.writer(room)
	if err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}

// Compact atomically rewrites the room log to its current minimal state.
func (s *FileStore) Compact(room string, state map[string]crdt.Register) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compactLocked(room, state)
}

func (s *FileStore) compactLocked(room string, state map[string]crdt.Register) error {
	dest := s.path(room)
	tmp := dest + ".tmp"
	if err := writeLog(tmp, state); err != nil {
		_ = os.Remove(tmp) // never leave a half-written snapshot behind
		return err
	}

	// Drop the open append handle so the rename takes effect for future writes.
	if old, ok := s.files[room]; ok {
		_ = old.Close()
		delete(s.files, room)
		delete(s.bufs, room)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeLog(path string, state map[string]crdt.Register) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for k, reg := range state {
		b, err := json.Marshal(Record{Key: k, Reg: reg})
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Close flushes and closes every open room file.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for room, w := range s.bufs {
		if err := w.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		if f := s.files[room]; f != nil {
			f.Close()
		}
	}
	s.files = map[string]*os.File{}
	s.bufs = map[string]*bufio.Writer{}
	return firstErr
}
