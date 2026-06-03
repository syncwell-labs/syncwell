// Package crdt implements the conflict-free replicated data types at the heart
// of Syncwell. The Phase-1 type is LWWMap: a state-based (CvRDT) last-writer-
// wins map whose Merge operation is commutative, associative, and idempotent,
// guaranteeing that any set of replicas that exchange state in any order, with
// duplicates or delays, converge to byte-identical state.
package crdt

import (
	"encoding/json"
	"sort"
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
// Higher timestamp wins; equal timestamps imply the same (counter,node) and
// therefore the same write, so either side is correct.
func (r Register) merge(other Register) Register {
	if other.TS.After(r.TS) {
		return other
	}
	return r
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
