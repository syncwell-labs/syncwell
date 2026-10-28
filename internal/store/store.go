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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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
	// Append records a single winning register write.
	Append(room string, rec Record) error
	// Compact rewrites a room's log to one line per current register, bounding
	// file growth. Atomic: writes a temp file then renames.
	Compact(room string, state map[string]crdt.Register) error
	// Close flushes and closes all open files.
	Close() error
}

// FsyncMode controls when appended records are forced to stable storage.
type FsyncMode int

const (
	// FsyncBatch fsyncs dirty rooms on a short interval. A power loss can lose
	// at most one interval of acknowledged writes; a process kill loses nothing
	// because every append is already flushed to the OS.
	FsyncBatch FsyncMode = iota
	// FsyncOS only flushes to the OS page cache. Fastest; survives process
	// kill but not machine crash / power loss.
	FsyncOS
	// FsyncEvery fsyncs on every append. Survives power loss, at the cost of
	// one disk flush per write.
	FsyncEvery
)

func (m FsyncMode) String() string {
	switch m {
	case FsyncOS:
		return "os"
	case FsyncEvery:
		return "every"
	default:
		return "batch"
	}
}

// ParseFsyncMode parses the operator-facing mode names. An empty string means
// the default (batch).
func ParseFsyncMode(s string) (FsyncMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "batch":
		return FsyncBatch, nil
	case "os", "none":
		return FsyncOS, nil
	case "every", "always":
		return FsyncEvery, nil
	}
	return 0, fmt.Errorf("invalid fsync mode %q (want os, batch, or every)", s)
}

// Options configures a FileStore.
type Options struct {
	Fsync           FsyncMode
	SyncInterval    time.Duration // batch mode interval; <=0 = 5ms
	MigrateLegacyV1 bool          // explicitly allow importing the ambiguous v1 sanitized filename layout
}

func (o Options) withDefaults() Options {
	if o.SyncInterval <= 0 {
		o.SyncInterval = 5 * time.Millisecond
	}
	return o
}

// ManifestFile is the name of the backup manifest inside a backup directory.
const ManifestFile = "MANIFEST.json"

// BackupFile describes one file in a backup.
type BackupFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BackupManifest is written next to the copied logs so a restore can be
// verified before it touches the live data directory.
type BackupManifest struct {
	Format  int          `json:"format"`
	Created time.Time    `json:"created"`
	Files   []BackupFile `json:"files"`
}

// FileStore is the default on-disk Store.
type FileStore struct {
	dir             string
	fsync           FsyncMode
	syncInterval    time.Duration
	migrateLegacyV1 bool

	mu    sync.Mutex
	files map[string]*os.File
	bufs  map[string]*bufio.Writer
	sizes map[string]int64
	dirty map[string]struct{}

	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// Open creates (if needed) the data directory and returns a FileStore with the
// default durability policy (batched fsync).
func Open(dir string) (*FileStore, error) {
	return OpenWithOptions(dir, Options{})
}

// OpenWithOptions is Open with an explicit durability policy.
func OpenWithOptions(dir string, opts Options) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	s := &FileStore{
		dir:             dir,
		fsync:           opts.Fsync,
		syncInterval:    opts.SyncInterval,
		migrateLegacyV1: opts.MigrateLegacyV1,
		files:           map[string]*os.File{},
		bufs:            map[string]*bufio.Writer{},
		sizes:           map[string]int64{},
		dirty:           map[string]struct{}{},
		stop:            make(chan struct{}),
		done:            make(chan struct{}),
	}
	go s.syncLoop()
	return s, nil
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

// LoadLimits bounds the state accepted from disk. A zero field is unlimited,
// except that a zero value for all fields retains the historical 16 MiB line
// cap. Limits are enforced while replaying so a corrupt or tampered log cannot
// force unbounded memory use before the room notices.
type LoadLimits struct {
	MaxEntries    int
	MaxBytes      int64
	MaxKeyBytes   int
	MaxValueBytes int
	MaxNodeBytes  int
}

func (l LoadLimits) maxLineBytes() int {
	const fallback = 16 * 1024 * 1024
	if l.MaxKeyBytes <= 0 && l.MaxValueBytes <= 0 && l.MaxNodeBytes <= 0 {
		return fallback
	}
	// JSON string escaping can expand a raw byte to six bytes.
	n := 6*(l.MaxKeyBytes+l.MaxValueBytes+l.MaxNodeBytes) + 64*1024
	if n < 64*1024 {
		n = 64 * 1024
	}
	if n > fallback {
		return fallback
	}
	return n
}

// Load replays the op-log through a fresh CRDT and returns its snapshot. A torn
// final line (from a crash mid-write) is skipped rather than failing the load.
func (s *FileStore) Load(room string) (map[string]crdt.Register, error) {
	return s.LoadWithLimits(room, LoadLimits{})
}

// LoadWithLimits is Load with explicit bounds on the accepted document. A state
// that exceeds the configured room budget is rejected instead of being adopted
// and immediately compacted over the original log.
func (s *FileStore) LoadWithLimits(room string, limits LoadLimits) (map[string]crdt.Register, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadWithLimitsLocked(room, limits)
}

func (s *FileStore) loadWithLimitsLocked(room string, limits LoadLimits) (map[string]crdt.Register, error) {
	snap, found, err := s.loadPath(s.path(room), limits)
	if err != nil || found {
		if err == nil && found {
			s.rememberSize(room)
		}
		return snap, err
	}

	// One-way migration from the base64 layout. That filename was injective,
	// so it is safe to import automatically.
	if alt := s.base64Path(room); alt != s.path(room) {
		if _, statErr := os.Stat(alt); statErr == nil {
			snap, found, err = s.loadPath(alt, limits)
			if err != nil {
				return nil, err
			}
			if found {
				if err := s.compactLocked(room, snap); err != nil {
					return nil, err
				}
				return snap, nil
			}
		}
	}

	// The v1 sanitized layout was not injective ("a/b" and "a_b" both became
	// "a_b.log"). Importing it automatically can silently attach one room's
	// history to another, so it is opt-in and still rejected for unsafe ids.
	if alt := s.legacyPath(room); alt != s.path(room) {
		if _, statErr := os.Stat(alt); statErr == nil {
			if legacyUnsafe.MatchString(room) {
				return nil, fmt.Errorf("legacy room file %s cannot be safely attributed to room %q; re-import it manually", alt, room)
			}
			if !s.migrateLegacyV1 {
				return nil, fmt.Errorf("legacy room file %s requires -migrate-legacy-v1 (SYNCWELL_MIGRATE_LEGACY_V1) after verifying that the id is unambiguous", alt)
			}
			snap, found, err = s.loadPath(alt, limits)
			if err != nil {
				return nil, err
			}
			if found {
				if err := s.compactLocked(room, snap); err != nil {
					return nil, err
				}
				return snap, nil
			}
		}
	}
	return map[string]crdt.Register{}, nil
}

func (s *FileStore) rememberSize(room string) {
	if fi, err := os.Stat(s.path(room)); err == nil {
		s.sizes[room] = fi.Size()
	}
}

func (s *FileStore) loadPath(path string, limits LoadLimits) (map[string]crdt.Register, bool, error) {
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
	sc.Buffer(make([]byte, 0, 64*1024), limits.maxLineBytes())
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
		if err := validateLoadedRecord(rec, limits); err != nil {
			return nil, true, fmt.Errorf("op-log %s: %w", path, err)
		}
		doc.ApplyRegister(rec.Key, rec.Reg)
		if entries, bytes := doc.Size(); (limits.MaxEntries > 0 && entries > limits.MaxEntries) ||
			(limits.MaxBytes > 0 && bytes > limits.MaxBytes) {
			return nil, true, fmt.Errorf("op-log %s exceeds the configured room limits (%d registers, %d bytes)", path, entries, bytes)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, true, err
	}
	return doc.Snapshot(), true, nil
}

func validateLoadedRecord(rec Record, limits LoadLimits) error {
	if rec.Key == "" || (limits.MaxKeyBytes > 0 && len(rec.Key) > limits.MaxKeyBytes) {
		return errors.New("invalid key in persisted record")
	}
	if rec.Reg.TS.Node == "" || (limits.MaxNodeBytes > 0 && len(rec.Reg.TS.Node) > limits.MaxNodeBytes) {
		return errors.New("invalid node id in persisted record")
	}
	if rec.Reg.Deleted {
		if len(rec.Reg.Value) > 0 {
			return errors.New("tombstone carries a value")
		}
		return nil
	}
	if len(rec.Reg.Value) == 0 || !json.Valid(rec.Reg.Value) ||
		(limits.MaxValueBytes > 0 && len(rec.Reg.Value) > limits.MaxValueBytes) {
		return errors.New("invalid value in persisted record")
	}
	return nil
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
	if fi, statErr := f.Stat(); statErr == nil {
		s.sizes[room] = fi.Size()
	}
	s.files[room] = f
	s.bufs[room] = w
	return w, nil
}

// Append writes one record according to the store's durability policy:
//
//   - every: flush + fsync before returning
//   - batch: flush immediately, fsync on the background interval (default)
//   - os:    flush immediately, never fsync
//
// In all modes the record is in the OS page cache when Append returns, so it
// survives a process kill. Only "every" (or a completed batch interval)
// guarantees it survives a machine crash or power loss.
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
	line := append(b, '\n')
	if _, err := w.Write(line); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	s.sizes[room] += int64(len(line))

	switch s.fsync {
	case FsyncEvery:
		f := s.files[room]
		if f == nil {
			return fmt.Errorf("store: missing file handle for room %q", room)
		}
		return f.Sync()
	case FsyncOS:
		return nil
	default:
		s.dirty[room] = struct{}{}
		return nil
	}
}

// LogSize reports the current append-log size for a room (0 if unknown). The
// room uses this to trigger compaction when a log exceeds its configured cap.
func (s *FileStore) LogSize(room string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sizes[room]
}

func (s *FileStore) syncLoop() {
	defer close(s.done)
	t := time.NewTicker(s.syncInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.syncDirty()
		case <-s.stop:
			s.syncDirty()
			return
		}
	}
}

func (s *FileStore) syncDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for room := range s.dirty {
		f := s.files[room]
		if f == nil {
			delete(s.dirty, room)
			continue
		}
		if err := f.Sync(); err != nil {
			continue // keep it dirty; retry on the next tick
		}
		delete(s.dirty, room)
	}
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
	delete(s.dirty, room)
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	if fi, err := os.Stat(dest); err == nil {
		s.sizes[room] = fi.Size()
	} else {
		delete(s.sizes, room)
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

// Backup copies every room log into dst and writes a checksummed manifest.
// Call it on a stopped server (or at least with no concurrent writers): it
// flushes and fsyncs open files before copying, but it does not freeze the
// process.
func (s *FileStore) Backup(dst string) (*BackupManifest, error) {
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

	if err := os.MkdirAll(dst, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	manifest := &BackupManifest{Format: 1, Created: time.Now().UTC()}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		bf, err := copyHashed(filepath.Join(dst, entry.Name()), filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		manifest.Files = append(manifest.Files, bf)
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Name < manifest.Files[j].Name })

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(dst, ManifestFile+".tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, filepath.Join(dst, ManifestFile)); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := syncDir(dst); err != nil {
		return nil, err
	}
	return manifest, nil
}

// VerifyBackup checks a backup directory against its manifest and returns the
// manifest. Any missing file, size mismatch, or checksum mismatch is an error.
func VerifyBackup(dir string) (*BackupManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, err
	}
	var m BackupManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid backup manifest: %w", err)
	}
	if m.Format != 1 {
		return nil, fmt.Errorf("unsupported backup format %d", m.Format)
	}
	for _, bf := range m.Files {
		if bf.Name == "" || filepath.Base(bf.Name) != bf.Name || !strings.HasSuffix(bf.Name, ".log") {
			return nil, fmt.Errorf("unsafe backup entry %q", bf.Name)
		}
		path := filepath.Join(dir, bf.Name)
		fi, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if fi.Size() != bf.Size {
			return nil, fmt.Errorf("backup file %s: size %d, manifest says %d", bf.Name, fi.Size(), bf.Size)
		}
		sum, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		if sum != bf.SHA256 {
			return nil, fmt.Errorf("backup file %s: checksum mismatch", bf.Name)
		}
	}
	return &m, nil
}

// Restore verifies src and replaces the room logs in dst with the backup's.
// Stale logs in dst are removed so a restore cannot mix old and backed-up
// state. The server must be stopped while restoring.
func Restore(src, dst string) (*BackupManifest, error) {
	m, err := VerifyBackup(src)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		if err := os.Remove(filepath.Join(dst, entry.Name())); err != nil {
			return nil, err
		}
	}
	for _, bf := range m.Files {
		if err := copyAtomic(filepath.Join(dst, bf.Name), filepath.Join(src, bf.Name)); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func copyHashed(dst, src string) (BackupFile, error) {
	in, err := os.Open(src)
	if err != nil {
		return BackupFile{}, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return BackupFile{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if err != nil {
		_ = out.Close()
		return BackupFile{}, err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return BackupFile{}, err
	}
	if err := out.Close(); err != nil {
		return BackupFile{}, err
	}
	return BackupFile{Name: filepath.Base(dst), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func copyAtomic(dst, src string) error {
	tmp := dst + ".restore.tmp"
	if _, err := copyHashed(tmp, src); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(dst))
}

// syncDir makes a rename durable across power loss.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Close flushes, fsyncs, and closes every open room file.
func (s *FileStore) Close() error {
	s.closeOnce.Do(func() { close(s.stop) })
	<-s.done

	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for room, w := range s.bufs {
		if err := w.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		if f := s.files[room]; f != nil {
			if err := f.Sync(); err != nil && firstErr == nil {
				firstErr = err
			}
			if err := f.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	s.files = map[string]*os.File{}
	s.bufs = map[string]*bufio.Writer{}
	s.dirty = map[string]struct{}{}
	return firstErr
}
