// Package crdt implements the conflict-free replicated data types at the heart
// of Syncwell. The Phase-1 type is LWWMap: a state-based (CvRDT) last-writer-
// wins map whose Merge operation is commutative, associative, and idempotent,
// guaranteeing that any set of replicas that exchange state in any order, with
// duplicates or delays, converge to byte-identical state.
package crdt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Register is a single LWW value cell: the value, the timestamp of the write
// that produced it, and a tombstone flag so that deletes participate in the
// same ordering as writes (a delete can win over a stale write and vice versa).
type Register struct {
	Value   json.RawMessage `json:"v,omitempty"`
	TS      Timestamp       `json:"t"`
	Deleted bool            `json:"d,omitempty"`
}

// merge returns the winning register between two views of the same key.
// Timestamp is the primary order. A deterministic register-content order is
// used when timestamps tie: clients can be buggy or malicious and submit two
// different values for the same (counter,node), and keeping whichever replica
// happened to see first would violate CRDT commutativity.
func (r Register) merge(other Register) Register {
	if other.after(r) {
		return other
	}
	return r
}

func (r Register) after(other Register) bool {
	if r.TS != other.TS {
		return r.TS.After(other.TS)
	}
	// A delete wins an otherwise identical-timestamp write. This is a
	// deterministic safety preference; a higher timestamp can still recreate
	// the key normally.
	if r.Deleted != other.Deleted {
		return r.Deleted
	}
	if cmp := bytes.Compare(canonicalJSON(r.Value), canonicalJSON(other.Value)); cmp != 0 {
		return cmp > 0
	}
	return bytes.Compare(r.Value, other.Value) > 0
}

// Equal reports byte-identical register equality.
func (r Register) Equal(other Register) bool {
	return r.TS == other.TS && r.Deleted == other.Deleted && bytes.Equal(r.Value, other.Value)
}

// ResolveRegister returns the CRDT winner between two registers.
func ResolveRegister(current, candidate Register) Register {
	return current.merge(candidate)
}

// canonicalJSON gives semantically equivalent JSON values one deterministic
// representation before they are used as a timestamp tie-breaker. RawMessage
// values arrive from JSON frames, but retaining a byte fallback keeps the CRDT
// total even for values supplied directly by an embedding application.
//
// The encoding follows RFC 8785 (JCS) so the client SDK's stableJSON produces
// the same bytes for the same value:
//   - numbers are IEEE-754 doubles serialized with ECMAScript's algorithm;
//   - object keys are sorted by UTF-16 code units;
//   - strings use minimal escaping (no HTML or U+2028/29 escaping);
//   - no insignificant whitespace.
func canonicalJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var value any
	if err := dec.Decode(&value); err != nil {
		return raw
	}
	if err := requireEOF(dec); err != nil {
		return raw
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, value); err != nil {
		return raw
	}
	return buf.Bytes()
}

func writeCanonical(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case float64:
		buf.WriteString(formatESNumber(v))
	case string:
		writeESString(buf, v)
	case []any:
		buf.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		// JCS sorts by UTF-16 code units, which differs from Go's default
		// byte/code-point ordering for astral-plane characters.
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeESString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, v[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical JSON: unsupported type %T", value)
	}
	return nil
}

func utf16Less(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	n := len(ua)
	if len(ub) < n {
		n = len(ub)
	}
	for i := 0; i < n; i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func writeESString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// formatESNumber implements ECMAScript Number::toString (the number format
// required by RFC 8785). strconv's 'g' format uses different thresholds and
// exponent padding, so the shortest 'e' form is reformatted by hand.
func formatESNumber(f float64) string {
	if f == 0 {
		return "0" // also normalizes -0
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	ePos := strings.IndexByte(s, 'e')
	if ePos < 0 {
		return s
	}
	digits := strings.Replace(s[:ePos], ".", "", 1)
	exp, err := strconv.Atoi(s[ePos+1:])
	if err != nil {
		return s
	}
	n := exp + 1
	k := len(digits)
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		if k == 1 {
			out = digits
		} else {
			out = digits[:1] + "." + digits[1:]
		}
		e := n - 1
		if e >= 0 {
			out += "e+" + strconv.Itoa(e)
		} else {
			out += "e-" + strconv.Itoa(-e)
		}
	}
	if neg {
		return "-" + out
	}
	return out
}

func requireEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	return err
}

// LWWMap is a state-based last-writer-wins map keyed by string. Values are
// opaque JSON, so the same engine drives any document shape (kanban card,
// text node, presence blob) without changing the merge logic.
type LWWMap struct {
	entries map[string]Register
}

// NewLWWMap returns an empty map.
func NewLWWMap() *LWWMap { return &LWWMap{entries: map[string]Register{}} }

// Set records a write at timestamp ts (which must come from the local Clock).
func (m *LWWMap) Set(key string, value json.RawMessage, ts Timestamp) {
	m.apply(key, Register{Value: value, TS: ts})
}

// Delete tombstones a key at timestamp ts.
func (m *LWWMap) Delete(key string, ts Timestamp) {
	m.apply(key, Register{TS: ts, Deleted: true})
}

func (m *LWWMap) apply(key string, candidate Register) {
	if cur, ok := m.entries[key]; ok {
		m.entries[key] = cur.merge(candidate)
		return
	}
	m.entries[key] = candidate
}

// Get returns the live value for key, or (nil,false) if absent or tombstoned.
func (m *LWWMap) Get(key string) (json.RawMessage, bool) {
	r, ok := m.entries[key]
	if !ok || r.Deleted {
		return nil, false
	}
	return r.Value, true
}

// Merge folds another replica's full state into this one. This is the CvRDT
// join: commutative, associative, and idempotent (proven by the test suite).
func (m *LWWMap) Merge(other *LWWMap) {
	for k, their := range other.entries {
		m.apply(k, their)
	}
}

// ApplyRegister merges a single incoming register for key — the per-key CRDT
// join. Used by the server to fold a client-authored write into the room doc,
// and by the op-log replay path.
func (m *LWWMap) ApplyRegister(key string, r Register) { m.apply(key, r) }

// Entry returns the raw register for key (including tombstones), if present.
func (m *LWWMap) Entry(key string) (Register, bool) {
	r, ok := m.entries[key]
	return r, ok
}

// Snapshot returns a copy of the raw register state, including tombstones, for
// persistence or for sending full state to a newly-connected client.
func (m *LWWMap) Snapshot() map[string]Register {
	out := make(map[string]Register, len(m.entries))
	for k, v := range m.entries {
		out[k] = v
	}
	return out
}

// Load replaces the map's state from a snapshot (used on restart / on join).
func (m *LWWMap) Load(snap map[string]Register) {
	m.entries = make(map[string]Register, len(snap))
	for k, v := range snap {
		m.entries[k] = v
	}
}

// Live returns only the non-deleted key/value pairs (the user-visible doc).
func (m *LWWMap) Live() map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, r := range m.entries {
		if !r.Deleted {
			out[k] = r.Value
		}
	}
	return out
}

// CollectTombstones removes tombstone entries whose timestamp counter is
// strictly below `below`, returning the number removed. `below` MUST be a
// causal-stability low-water mark — a counter no future write can fall beneath —
// otherwise a delayed stale write could resurrect a collected key. See
// DESIGN.md §4 for how the server computes that bound.
func (m *LWWMap) CollectTombstones(below uint64) int {
	n := 0
	for k, r := range m.entries {
		if r.Deleted && r.TS.Counter < below {
			delete(m.entries, k)
			n++
		}
	}
	return n
}

// Stats reports live vs. tombstone entry counts.
type Stats struct {
	Live       int `json:"live"`
	Tombstones int `json:"tombstones"`
}

// Stats returns the current live/tombstone breakdown.
func (m *LWWMap) Stats() Stats {
	var s Stats
	for _, r := range m.entries {
		if r.Deleted {
			s.Tombstones++
		} else {
			s.Live++
		}
	}
	return s
}

// Keys returns the sorted live keys for deterministic iteration.
func (m *LWWMap) Keys() []string {
	ks := make([]string, 0, len(m.entries))
	for k, r := range m.entries {
		if !r.Deleted {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	return ks
}
