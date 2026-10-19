package crdt

import (
	"encoding/json"
	"reflect"
	"testing"
)

func val(s string) json.RawMessage { b, _ := json.Marshal(s); return b }

func mustGet(t *testing.T, m *LWWMap, key string) string {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("expected key %q to be live", key)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		t.Fatalf("unmarshal %q: %v", key, err)
	}
	return s
}

func clone(m *LWWMap) *LWWMap {
	c := NewLWWMap()
	c.Merge(m)
	return c
}

// TestLastWriterWins: the higher timestamp overwrites, regardless of apply order.
func TestLastWriterWins(t *testing.T) {
	m := NewLWWMap()
	m.Set("title", val("old"), Timestamp{Counter: 1, Node: "A"})
	m.Set("title", val("new"), Timestamp{Counter: 2, Node: "A"})
	if got := mustGet(t, m, "title"); got != "new" {
		t.Fatalf("want new, got %q", got)
	}
	// A stale write (lower ts) must NOT clobber the newer value.
	m.Set("title", val("stale"), Timestamp{Counter: 1, Node: "Z"})
	if got := mustGet(t, m, "title"); got != "new" {
		t.Fatalf("stale write won; got %q", got)
	}
}

// TestTombstoneOrdering: delete and write compete on timestamp like any write.
func TestTombstoneOrdering(t *testing.T) {
	m := NewLWWMap()
	m.Set("k", val("v"), Timestamp{Counter: 1, Node: "A"})
	m.Delete("k", Timestamp{Counter: 2, Node: "A"}) // newer delete wins
	if _, ok := m.Get("k"); ok {
		t.Fatal("expected k deleted")
	}
	m.Set("k", val("resurrected"), Timestamp{Counter: 3, Node: "A"}) // newer write wins back
	if got := mustGet(t, m, "k"); got != "resurrected" {
		t.Fatalf("want resurrected, got %q", got)
	}
	// A stale delete must not remove a newer value.
	m.Delete("k", Timestamp{Counter: 1, Node: "A"})
	if got := mustGet(t, m, "k"); got != "resurrected" {
		t.Fatalf("stale delete won; got %q", got)
	}
}

// TestMergeIdempotent: merging a replica with itself changes nothing (x∨x=x).
func TestMergeIdempotent(t *testing.T) {
	a := NewLWWMap()
	a.Set("x", val("1"), Timestamp{Counter: 1, Node: "A"})
	a.Set("y", val("2"), Timestamp{Counter: 2, Node: "A"})
	before := a.Snapshot()
	a.Merge(clone(a))
	if !reflect.DeepEqual(before, a.Snapshot()) {
		t.Fatal("merge is not idempotent")
	}
}

// TestMergeCommutative: merge(a,b) == merge(b,a) (x∨y = y∨x).
func TestMergeCommutative(t *testing.T) {
	a := NewLWWMap()
	a.Set("k", val("a-wins"), Timestamp{Counter: 5, Node: "A"})
	a.Set("only-a", val("1"), Timestamp{Counter: 1, Node: "A"})

	b := NewLWWMap()
	b.Set("k", val("b-loses"), Timestamp{Counter: 3, Node: "B"})
	b.Set("only-b", val("2"), Timestamp{Counter: 1, Node: "B"})

	ab := clone(a)
	ab.Merge(b)
	ba := clone(b)
	ba.Merge(a)

	if !reflect.DeepEqual(ab.Snapshot(), ba.Snapshot()) {
		t.Fatal("merge is not commutative")
	}
	if got := mustGet(t, ab, "k"); got != "a-wins" {
		t.Fatalf("want a-wins, got %q", got)
	}
}

// Equal timestamps must still converge. Timestamps are client-authored, so a
// buggy or malicious client can reuse one with a different value.
func TestMergeCommutativeWithConflictingEqualTimestamp(t *testing.T) {
	ts := Timestamp{Counter: 9, Node: "same-client"}
	a := NewLWWMap()
	a.Set("k", val("alpha"), ts)
	b := NewLWWMap()
	b.Set("k", val("bravo"), ts)

	ab := clone(a)
	ab.Merge(b)
	ba := clone(b)
	ba.Merge(a)

	if !reflect.DeepEqual(ab.Snapshot(), ba.Snapshot()) {
		t.Fatalf("equal-timestamp registers diverged: ab=%v ba=%v", ab.Snapshot(), ba.Snapshot())
	}
	if got := mustGet(t, ab, "k"); got != "bravo" {
		t.Fatalf("deterministic tie-break chose %q, want bravo", got)
	}
}

func TestDeleteWinsEqualTimestampTie(t *testing.T) {
	ts := Timestamp{Counter: 9, Node: "same-client"}
	a := NewLWWMap()
	a.Set("k", val("value"), ts)
	b := NewLWWMap()
	b.Delete("k", ts)
	a.Merge(b)
	if _, ok := a.Get("k"); ok {
		t.Fatal("delete must deterministically win an equal-timestamp tie")
	}
}

// TestMergeAssociative: (a∨b)∨c == a∨(b∨c).
func TestMergeAssociative(t *testing.T) {
	a := NewLWWMap()
	a.Set("k", val("a"), Timestamp{Counter: 1, Node: "A"})
	b := NewLWWMap()
	b.Set("k", val("b"), Timestamp{Counter: 2, Node: "B"})
	c := NewLWWMap()
	c.Set("k", val("c"), Timestamp{Counter: 3, Node: "C"})

	left := clone(a)
	left.Merge(b)
	left.Merge(c)

	bc := clone(b)
	bc.Merge(c)
	right := clone(a)
	right.Merge(bc)

	if !reflect.DeepEqual(left.Snapshot(), right.Snapshot()) {
		t.Fatal("merge is not associative")
	}
}

// TestConvergenceUnderConcurrentEdits is the headline property: two replicas
// edit while "partitioned" (neither sees the other's clock), then exchange
// full state. After a single round of cross-merge they must be byte-identical.
func TestConvergenceUnderConcurrentEdits(t *testing.T) {
	ca := NewClock("A")
	cb := NewClock("B")

	a := NewLWWMap()
	b := NewLWWMap()

	// Concurrent, conflicting edits to the same keys plus disjoint edits.
	a.Set("title", val("A's title"), ca.Tick())
	a.Set("a-note", val("hello from A"), ca.Tick())
	a.Delete("shared", ca.Tick())

	b.Set("title", val("B's title"), cb.Tick())
	b.Set("b-note", val("hello from B"), cb.Tick())
	b.Set("shared", val("B keeps shared"), cb.Tick())

	// Exchange state (order/duplication shouldn't matter — merge twice).
	a.Merge(b)
	b.Merge(a)
	a.Merge(b) // duplicate delivery must be harmless

	if !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
		t.Fatalf("replicas diverged:\nA=%v\nB=%v", a.Snapshot(), b.Snapshot())
	}
	// Disjoint edits survive on both sides.
	if mustGet(t, a, "a-note") != "hello from A" || mustGet(t, a, "b-note") != "hello from B" {
		t.Fatal("disjoint edits lost")
	}
}
