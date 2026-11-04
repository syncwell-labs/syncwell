package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"syncwell/internal/crdt"
	"syncwell/internal/store"
)

type oversizedLoadStore struct {
	compactions int
}

func (s *oversizedLoadStore) Load(string) (map[string]crdt.Register, error) {
	value, _ := json.Marshal(strings.Repeat("x", 300*1024))
	return map[string]crdt.Register{
		"k": {Value: value, TS: crdt.Timestamp{Counter: 1, Node: "n"}},
	}, nil
}
func (s *oversizedLoadStore) Append(string, store.Record) error { return nil }
func (s *oversizedLoadStore) Compact(string, map[string]crdt.Register) error {
	s.compactions++
	return nil
}
func (s *oversizedLoadStore) Close() error        { return nil }
func (s *oversizedLoadStore) Delete(string) error { return nil }

func TestLoadPersistedRejectsStateBeyondLimitsAndSkipsCompaction(t *testing.T) {
	st := &oversizedLoadStore{}
	r := &Room{
		id:     "r",
		doc:    crdt.NewLWWMap(),
		clock:  crdt.NewClock("server:r"),
		limits: Limits{}.withDefaults(),
		store:  st,
	}
	r.loadPersisted()
	if r.loadErr == nil {
		t.Fatal("loadPersisted accepted a value larger than MaxValueBytes")
	}
	if entries, _ := r.doc.Size(); entries != 0 {
		t.Fatalf("doc adopted %d registers from rejected state", entries)
	}
	if _, err := r.applyAndPersist("k", crdt.Register{Value: json.RawMessage(`"v"`), TS: crdt.Timestamp{Counter: 2, Node: "n"}}); !errors.Is(err, errLoadFailed) {
		t.Fatalf("applyAndPersist error = %v, want errLoadFailed", err)
	}
	r.maintain()
	if st.compactions != 0 {
		t.Fatalf("compaction ran %d times after a failed load; original log could be overwritten", st.compactions)
	}
	if !r.evictIfEmpty() {
		t.Fatal("room with no clients was not evicted")
	}
	if st.compactions != 0 {
		t.Fatalf("idle eviction compacted over a rejected log (%d compactions)", st.compactions)
	}
}

type failedLoadStore struct{ oversizedLoadStore }

func (s *failedLoadStore) LoadWithLimits(string, store.LoadLimits) (map[string]crdt.Register, error) {
	return nil, errors.New("limit exceeded")
}

func TestLoadPersistedPropagatesLimitedLoaderError(t *testing.T) {
	r := &Room{
		id:     "r",
		doc:    crdt.NewLWWMap(),
		clock:  crdt.NewClock("server:r"),
		limits: Limits{}.withDefaults(),
		store:  &failedLoadStore{},
	}
	r.loadPersisted()
	if r.loadErr == nil {
		t.Fatal("loadPersisted ignored LoadWithLimits error")
	}
}
