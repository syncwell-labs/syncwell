package server

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"sort"
	"strconv"

	"syncwell/internal/crdt"
)

// DigestRegisters hashes the sorted key/timestamp/value-hash tuples for a room.
func DigestRegisters(state map[string]crdt.Register) string {
	h := sha256.New()
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		reg := state[key]
		writeDigestPart(h, []byte(key))
		writeDigestPart(h, []byte(strconv.FormatUint(reg.TS.Counter, 10)))
		writeDigestPart(h, []byte(reg.TS.Node))
		value := sha256.Sum256(reg.Value)
		writeDigestPart(h, value[:])
		if reg.Deleted {
			writeDigestPart(h, []byte{1})
		} else {
			writeDigestPart(h, []byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeDigestPart(h hash.Hash, value []byte) {
	var size [8]byte
	n := uint64(len(value))
	for i := 7; i >= 0; i-- {
		size[i] = byte(n)
		n >>= 8
	}
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

type clusterDigestRequest struct {
	Room    string            `json:"room"`
	From    string            `json:"from"`
	Digest  string            `json:"digest"`
	Cursor  string            `json:"cursor,omitempty"`
	Entries map[string]string `json:"entries,omitempty"`
}
type clusterDigestResponse struct {
	Digest   string                   `json:"digest"`
	Entries  map[string]string        `json:"entries,omitempty"`
	Missing  map[string]crdt.Register `json:"missing,omitempty"`
	TooLarge bool                     `json:"tooLarge,omitempty"`
}

func digestEntry(reg crdt.Register) string {
	h := sha256.New()
	writeDigestPart(h, []byte(strconv.FormatUint(reg.TS.Counter, 10)))
	writeDigestPart(h, []byte(reg.TS.Node))
	value := sha256.Sum256(reg.Value)
	writeDigestPart(h, value[:])
	if reg.Deleted {
		writeDigestPart(h, []byte{1})
	} else {
		writeDigestPart(h, []byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func digestEntries(state map[string]crdt.Register) map[string]string {
	entries := make(map[string]string, len(state))
	for key, reg := range state {
		entries[key] = digestEntry(reg)
	}
	return entries
}

const digestPageSize = 128

func digestPage(state map[string]crdt.Register, cursor string) map[string]crdt.Register {
	keys := make([]string, 0, len(state))
	for key := range state {
		if key > cursor {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > digestPageSize {
		keys = keys[:digestPageSize]
	}
	page := make(map[string]crdt.Register, len(keys))
	for _, key := range keys {
		page[key] = state[key]
	}
	return page
}

func lastDigestKey(m map[string]crdt.Register) string {
	last := ""
	for key := range m {
		if key > last {
			last = key
		}
	}
	return last
}

func (r *Room) currentDigest() string { return DigestRegisters(r.doc.Snapshot()) }
