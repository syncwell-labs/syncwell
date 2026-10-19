package sequence

import "testing"

func cloneDoc(d *Doc) *Doc {
	out := New()
	out.Merge(d)
	return out
}

func TestSequenceMergeIsCommutativeAndIdempotent(t *testing.T) {
	a := New()
	a.Insert("a1", "", "H")
	a.Insert("a2", "a1", "i")

	b := New()
	b.Insert("b1", "", "!")
	b.Insert("b2", "b1", " ")

	ab := cloneDoc(a)
	ab.Merge(b)
	ab.Merge(b)

	ba := cloneDoc(b)
	ba.Merge(a)

	if got, want := ab.Text(), ba.Text(); got != want {
		t.Fatalf("merge not commutative: %q vs %q", got, want)
	}
	if got, want := ab.Text(), "Hi! "; got != want {
		t.Fatalf("unexpected render order: got %q want %q", got, want)
	}
}

func TestSequenceConcurrentInsertsSortDeterministically(t *testing.T) {
	a := New()
	a.Insert("a1", "", "A")
	a.Insert("a2", "a1", "2")

	b := New()
	b.Insert("b1", "", "B")
	b.Insert("b2", "b1", "1")

	ab := cloneDoc(a)
	ab.Merge(b)

	if got, want := ab.Text(), "A2B1"; got != want {
		t.Fatalf("unexpected merged text: got %q want %q", got, want)
	}
}

func TestSequenceDeleteTombstonesVisibleNode(t *testing.T) {
	d := New()
	d.Insert("root", "", "x")
	d.Delete("root")
	if got := d.Text(); got != "" {
		t.Fatalf("deleted node remained visible: %q", got)
	}
}
