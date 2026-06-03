package crdt

import "testing"

// TestCollectTombstones verifies that GC removes only stable tombstones (below
// the low-water mark) and never touches live entries or recent tombstones.
func TestCollectTombstones(t *testing.T) {
	m := NewLWWMap()
	m.Set("live-old", val("a"), Timestamp{Counter: 2, Node: "A"})
	m.Delete("dead-old", Timestamp{Counter: 3, Node: "A"})   // stable tombstone
	m.Delete("dead-recent", Timestamp{Counter: 9, Node: "A"}) // above low-water
	m.Set("live-recent", val("b"), Timestamp{Counter: 8, Node: "A"})

	if got := m.Stats(); got.Live != 2 || got.Tombstones != 2 {
		t.Fatalf("pre-gc stats wrong: %+v", got)
	}

	// Low-water mark = 5: only tombstones with counter < 5 are collectible.
	removed := m.CollectTombstones(5)
	if removed != 1 {
		t.Fatalf("want 1 tombstone collected, got %d", removed)
	}

	if _, ok := m.Entry("dead-old"); ok {
		t.Fatal("stable tombstone should be gone")
	}
	if _, ok := m.Entry("dead-recent"); !ok {
		t.Fatal("recent tombstone must be retained (could still lose to a stale write)")
	}
	if v, ok := m.Get("live-recent"); !ok || string(v) != `"b"` {
		t.Fatal("live entries must be untouched")
	}
	if got := m.Stats(); got.Live != 2 || got.Tombstones != 1 {
		t.Fatalf("post-gc stats wrong: %+v", got)
	}
}
