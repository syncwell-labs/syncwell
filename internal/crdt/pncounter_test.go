package crdt

import (
	"encoding/json"
	"testing"
)

func TestPNCounter_BasicOperations(t *testing.T) {
	c := NewPNCounter("node-1")
	if c.Node() != "node-1" {
		t.Fatalf("expected node-1, got %q", c.Node())
	}
	if v := c.Value(); v != 0 {
		t.Fatalf("expected initial value 0, got %d", v)
	}

	c.Inc()
	c.Inc()
	c.Add(5)
	if v := c.Value(); v != 7 {
		t.Fatalf("expected value 7, got %d", v)
	}

	c.Dec()
	c.Sub(3)
	if v := c.Value(); v != 3 {
		t.Fatalf("expected value 3, got %d", v)
	}
}

func TestPNCounter_CRDTLaws(t *testing.T) {
	// Law 1: Commutativity A * B == B * A
	a := NewPNCounter("node-a")
	b := NewPNCounter("node-b")

	a.Add(10)
	a.Sub(2)

	b.Add(20)
	b.Sub(5)

	aClone := a.Clone()
	bClone := b.Clone()

	a.Merge(b)
	bClone.Merge(aClone)

	if !a.Equal(bClone) {
		t.Fatalf("PNCounter merge is not commutative: a=%+v, b=%+v", a.State(), bClone.State())
	}
	if a.Value() != (10 - 2 + 20 - 5) {
		t.Fatalf("expected value 23, got %d", a.Value())
	}

	// Law 2: Idempotence A * A == A
	priorState := a.State()
	modified := a.Merge(a)
	if modified {
		t.Fatal("merging self should not report modified")
	}
	afterState := a.State()
	if len(priorState.P) != len(afterState.P) || len(priorState.N) != len(afterState.N) {
		t.Fatal("idempotence violated")
	}

	// Law 3: Associativity (A * B) * C == A * (B * C)
	c := NewPNCounter("node-c")
	c.Add(100)
	c.Sub(30)

	// Left: (A * B) * C
	left := aClone.Clone()
	left.Merge(b)
	left.Merge(c)

	// Right: A * (B * C)
	bAndC := b.Clone()
	bAndC.Merge(c)
	right := aClone.Clone()
	right.Merge(bAndC)

	if !left.Equal(right) {
		t.Fatalf("associativity violated: left=%+v, right=%+v", left.State(), right.State())
	}
	if left.Value() != right.Value() {
		t.Fatalf("values differ: %d vs %d", left.Value(), right.Value())
	}
}

func TestPNCounter_JSONSerialization(t *testing.T) {
	c := NewPNCounter("node-1")
	c.Add(42)
	c.Sub(12)

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	c2 := NewPNCounter("node-2")
	if err := json.Unmarshal(data, c2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if c2.Value() != 30 {
		t.Fatalf("expected unmarshaled value 30, got %d", c2.Value())
	}

	// Test invalid JSON
	var bad PNCounter
	if err := bad.UnmarshalJSON([]byte("bad json")); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestPNCounter_EdgeCases(t *testing.T) {
	c := NewPNCounter("node-1")
	if c.Merge(nil) {
		t.Fatal("merging nil should return false")
	}
	if c.Equal(nil) {
		t.Fatal("comparing with nil should return false")
	}

	other := NewPNCounter("node-2")
	other.Add(5)
	if c.Equal(other) {
		t.Fatal("different counters should not be equal")
	}
}
