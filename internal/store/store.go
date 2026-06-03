// Package store persists room documents so a server survives restarts. The
// implementation is intentionally dependency-light — an append-only op-log file
// per room plus periodic snapshot compaction — to preserve the single-binary,
// no-external-database story. Each log line is one CRDT register; replaying the
// log in any order reconstructs the document, because the merge is a CRDT join.
package store

import (
	"bufio"
	"encoding/json"
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{
		dir:   dir,
		files: map[string]*os.File{},
		bufs:  map[string]*bufio.Writer{},
	}, nil
}

// roomFile sanitizes a room id into a safe filename (no path traversal).
var unsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func (s *FileStore) path(room string) string {
	return filepath.Join(s.dir, unsafe.ReplaceAllString(room, "_")+".log")
}

// Load replays the op-log through a fresh CRDT and returns its snapshot. A torn
// final line (from a crash mid-write) is skipped rather than failing the load.
func (s *FileStore) Load(room string) (map[string]crdt.Register, error) {
	f, err := os.Open(s.path(room))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]crdt.Register{}, nil
		}
		return nil, err
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
			continue // tolerate a torn last line written during kill -9
		}
		doc.ApplyRegister(rec.Key, rec.Reg)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return doc.Snapshot(), nil
}

func (s *FileStore) writer(room string) (*bufio.Writer, error) {
	if w, ok := s.bufs[room]; ok {
		return w, nil
	}
	f, err := os.OpenFile(s.path(room), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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

	tmp := s.path(room) + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for k, reg := range state {
		b, err := json.Marshal(Record{Key: k, Reg: reg})
		if err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// Drop the open append handle so the rename takes effect for future writes.
	if old, ok := s.files[room]; ok {
		old.Close()
		delete(s.files, room)
		delete(s.bufs, room)
	}
	return os.Rename(tmp, s.path(room))
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
