package crdt

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestChaosConvergence is the adversarial convergence proof. It runs many
// replicas issuing randomized concurrent writes/deletes, delivers operations
// with simulated partitions (partial mid-run delivery), then heals by replaying
// every operation to every replica in a SHUFFLED order and TWICE (duplicates).
// No matter the delivery schedule, all replicas must end byte-identical to a
// reference replica that saw every op exactly once — the CRDT guarantee.
//
// The run is fully deterministic (seeded), so a failure reproduces exactly.
func TestChaosConvergence(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1337, 90210} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runChaos(t, seed)
		})
	}
}

func runChaos(t *testing.T, seed int64) {
	const (
		replicas = 5
		keyspace = 8
		ops      = 3000
	)
	rng := rand.New(rand.NewSource(seed))

	docs := make([]*LWWMap, replicas)
	clocks := make([]*Clock, replicas)
	for i := range docs {
		docs[i] = NewLWWMap()
		clocks[i] = NewClock(fmt.Sprintf("r%d", i))
	}

	type op struct {
		key string
		reg Register
	}
	journal := make([]op, 0, ops)
	reference := NewLWWMap()

	for n := 0; n < ops; n++ {
		author := rng.Intn(replicas)
		key := fmt.Sprintf("k%d", rng.Intn(keyspace))

		// Sometimes observe a peer's view of this key first, entangling causality.
		if rng.Intn(3) == 0 {
			peer := rng.Intn(replicas)
			if r, ok := docs[peer].Entry(key); ok {
				clocks[author].Observe(r.TS)
			}
		}

		ts := clocks[author].Tick()
		var reg Register
		if rng.Intn(4) == 0 {
			reg = Register{TS: ts, Deleted: true}
		} else {
			v, _ := json.Marshal(fmt.Sprintf("r%d-n%d", author, n))
			reg = Register{Value: v, TS: ts}
		}

		docs[author].ApplyRegister(key, reg)
		reference.ApplyRegister(key, reg)
		journal = append(journal, op{key, reg})

		// Partition simulation: deliver this op to a random subset right now,
		// leaving the rest divergent until the heal phase.
		for i := 0; i < replicas; i++ {
			if i != author && rng.Intn(2) == 0 {
				docs[i].ApplyRegister(key, reg)
			}
		}
	}

	// Heal: every replica replays the entire journal in a shuffled order, twice,
	// exercising reordering and duplicate delivery.
	for i := range docs {
		order := rng.Perm(len(journal))
		for pass := 0; pass < 2; pass++ {
			for _, idx := range order {
				docs[i].ApplyRegister(journal[idx].key, journal[idx].reg)
			}
		}
	}

	want := reference.Snapshot()
	for i := range docs {
		if got := docs[i].Snapshot(); !reflect.DeepEqual(got, want) {
			t.Fatalf("replica %d diverged from reference after heal (seed %d)", i, seed)
		}
	}
}
