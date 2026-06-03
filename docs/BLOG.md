# Building a self-hostable real-time collaboration engine in Go

*(Draft for a Show HN / dev.to launch post. Edit to taste before publishing.)*

Every product eventually wants multiplayer: live cursors, presence, shared state
that just works when two people edit at once. The good hosted options
(Liveblocks, PartyKit) are closed and per-seat priced. I wanted to understand the
hard parts myself and end up with something I could self-host, so I built
**Syncwell** — an MIT-licensed collaboration engine that compiles to a single
~8 MB binary.

This post is about the three problems that actually make real-time
collaboration hard, and the design decisions behind each.

## 1. Merging concurrent edits without conflicts

Two users edit the same field at the same time. Who wins, and how do all replicas
agree? The two mainstream answers are **Operational Transform** (what classic
Google Docs uses) and **CRDTs**.

I chose a **CRDT** for one decisive reason: testability. A CRDT's correctness
reduces to three algebraic laws — the merge must be commutative, associative, and
idempotent. That's something you can *prove with property tests*:

```
merge(a, b) == merge(b, a)          // order of delivery doesn't matter
merge(merge(a,b),c) == merge(a,(b,c)) // grouping doesn't matter
merge(a, a) == a                     // duplicate delivery is harmless
```

OT's correctness lives in transform functions that are famously easy to get
subtly wrong. Syncwell starts with a **state-based last-writer-wins map**: each
key holds a value, a logical timestamp, and a tombstone flag. Idempotent +
commutative merge means a duplicated or reordered network message can never
corrupt state — which, as we'll see, is what makes reconnection trivial.

## 2. "Last writer wins" — but whose clock?

Using wall-clock time to decide the winner invites clock-skew bugs: the replica
with the fast clock wins everything. Syncwell uses **Lamport logical clocks** with
a node id for tie-breaking, giving a total order over all writes without
synchronized clocks.

The honest trade-off: "last" means *last in causal order*, not last in real time.
A client that made several edits while offline can have a higher logical counter
than a concurrent online edit, and win. For a collaboration tool that's the right
call — no edit is silently lost, and every replica resolves the conflict
identically.

## 3. Offline editing and reconnection

This is where it gets real. The trick: **the client holds its own CRDT replica.**
Writes apply locally first (instant UI, works with no network), then sync. On
reconnect, the client ships its full state, the server merges it, and sends back a
snapshot. Because merge is idempotent and commutative, **no replay log or
acknowledgement protocol is needed** — re-merging a snapshot simply can't corrupt
anything.

I proved this end-to-end with a test that cuts a client's socket, edits on both
sides, reconnects, and asserts byte-for-byte convergence. And a chaos test runs 5
replicas through 3000 randomized ops with simulated partitions, reordering, and
duplicate delivery — they always converge.

## Bonus: the parts nobody mentions

- **Memory.** Tombstones (deletes) can't be removed immediately — a delete must be
  able to beat a concurrent stale write. So a long-lived document grows forever…
  unless you garbage-collect. Syncwell tracks a **causal-stability low-water mark**
  (the min logical clock across connected replicas) and collects tombstones below
  it. In a test, 40 deletes collapse to ~1.
- **Concurrency.** Each room is owned by a single goroutine — the only code that
  touches the document. No mutexes, no data races by construction, just message
  passing.
- **Durability.** An append-only op-log per room, tolerant of a torn final line
  from a hard kill, compacted periodically. Survives `kill -9`.

## Numbers

50 concurrent clients in one room, single box: **p50 1.3 ms / p99 3.6 ms** sync
latency, ~9,000 messages/sec, **11.5 MB** server RSS.

## Try it

```bash
go run ./cmd/syncwell
# open http://localhost:8080/kanban.html in two windows
```

Code, design doc, and the test suite that proves all of the above:
**<link to repo>**. Feedback welcome — especially on the GC low-water-mark
approach and the offline conflict semantics.
