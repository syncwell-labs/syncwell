package server

import "testing"

func TestShardPlannerOwnerIsStableRegardlessOfInputOrder(t *testing.T) {
	a := NewShardPlanner("node-c", "node-a", "node-b", "node-a")
	b := NewShardPlanner("node-b", "node-c", "node-a")

	const room = "demo"
	if got, want := a.Owner(room), b.Owner(room); got != want {
		t.Fatalf("owner changed across equivalent node sets: %q vs %q", got, want)
	}
	if got := a.Owner(room); got == "" {
		t.Fatal("expected a real owner when nodes are configured")
	}
}

func TestShardPlannerOwnsMatchesComputedOwner(t *testing.T) {
	p := NewShardPlanner("node-a", "node-b")
	owner := p.Owner("kanban")
	if !p.Owns("kanban", owner) {
		t.Fatalf("expected planner to own room on %q", owner)
	}
	if owner == "node-a" && p.Owns("kanban", "node-b") {
		t.Fatal("unexpected secondary owner match")
	}
}

func TestShardPlannerEmptyMeansSingleNodeMode(t *testing.T) {
	p := NewShardPlanner()
	if got := p.Owner("room"); got != "" {
		t.Fatalf("expected empty owner in single-node mode, got %q", got)
	}
	if !p.Owns("room", "anything") {
		t.Fatal("single-node mode should not reject ownership")
	}
}
