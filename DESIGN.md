# Syncwell — Design & Trade-offs

This document records *why* Syncwell is built the way it is. It is deliberately
opinionated: every choice below has an alternative that was rejected for a
reason, and the costs are stated, not hidden.

---

## 1. CRDT vs. Operational Transform

The two mainstream ways to merge concurrent edits are **Operational Transform
(OT)** and **Conflict-free Replicated Data Types (CRDTs)**.

| | OT (Google Docs classic) | CRDT (Syncwell) |
|---|---|---|
| Correctness model | transform each op against concurrent ops | algebraic: merge is a commutative/associative/idempotent join |
| Needs a central server to order ops | Yes, essentially always | No — peers can merge in any order |
| Offline / P2P friendly | Hard | Natural |
| Implementation risk | Transform functions are notoriously hard to get right | Merge law is small and testable |
| Cost | Less metadata | Metadata/tombstone overhead |

**Decision: CRDT.** The deciding factor is *offline support and testability*.
A CRDT's correctness reduces to three algebraic laws, which we can prove with
property tests (see `internal/crdt/lwwmap_test.go`). OT's correctness lives in
transform functions that are easy to get subtly wrong and hard to test
exhaustively. The cost we accept is metadata overhead and tombstones (§4).

## 2. Why a state-based (CvRDT) LWW-Map first

CRDTs come in two flavors: **operation-based** (CmRDT — ship each op, requires
exactly-once causal delivery) and **state-based** (CvRDT — ship merged state,
tolerates dup/reorder/loss).

We start state-based because it is **robust to an unreliable network for free**:
`Merge` is idempotent and commutative, so a duplicated or out-of-order delivery
cannot corrupt state. That property is exactly what makes reconnection safe — a
client that missed messages just re-merges a fresh snapshot.

The first concrete type is a **Last-Writer-Wins Map** keyed by string with
opaque JSON values. One engine then drives any document shape (kanban card,
text field, presence blob) without changing the merge logic.

The three laws, all covered by tests:

- **Commutative:** `merge(a,b) == merge(b,a)` — order of delivery is irrelevant.
- **Associative:** `merge(merge(a,b),c) == merge(a,merge(b,c))` — grouping is irrelevant.
- **Idempotent:** `merge(a,a) == a` — duplicate delivery is harmless.

Together these guarantee **convergence**: any replicas that have seen the same
set of writes are byte-identical, regardless of how messages were delivered.

## 3. Ordering: Lamport timestamps, not wall clocks

"Last writer wins" needs a definition of "last." Using wall-clock time invites
**clock-skew bugs**: a replica with a fast clock wins every conflict.

Syncwell uses a **Lamport logical clock** plus a node id for tie-breaking
(`internal/crdt/clock.go`). Ordering is `(counter, node)`, giving a total order
over all writes without synchronized clocks.

**The honest cost:** "last writer" means "last in causal order," which is not
always "last in real time" when two replicas edit while partitioned. For a
collaboration tool this is acceptable — and far better than silent data loss
from clock skew. A future option is a Hybrid Logical Clock (HLC) to keep ordering
close to wall-clock time while staying skew-safe.

## 4. The tombstone-growth problem (the thing that breaks at scale)

Deletes are **tombstones**, not removals — a delete is a write that must be able
to win against a concurrent or stale set, so it has to linger. The cost: a
document that is edited and cleared forever **grows forever**. This is the
classic CRDT space problem and the first thing that breaks under sustained load.

**Planned fix (Phase 2):** snapshot + op-log persistence with **stability-based
garbage collection** — once every connected replica has acknowledged a
timestamp `T`, no future write can causally precede `T`, so tombstones older
than `T` are safe to collect. This requires tracking a per-replica low-water
mark and is intentionally deferred until persistence exists.

## 5. Concurrency model: single-owner goroutine per room, no locks

Each room is owned by **one goroutine** (`Room.loop` in `internal/server/hub.go`).
It is the only code that reads or writes the document, the clock, and the client
set. Everything else — websocket read pumps — communicates with it by sending
`command` values on a channel.

Consequences:

- **No mutexes on document state.** Data races are impossible by construction,
  not by careful locking.
- **A clear ownership rule:** only the room goroutine closes a client's `send`
  channel. Read pumps never close it; they enqueue a `leave` command. This
  single invariant removes the classic "send on closed channel" / double-close
  bug class.
- **Back-pressure is explicit:** if a client's send buffer is full, the room
  evicts it rather than blocking every other peer behind one slow consumer.

The trade-off is that one room's throughput is bounded by one core. That is fine
until a single document is extremely hot; horizontal scaling (sharding rooms
across nodes, then across processes) is the stretch goal in the roadmap.

## 6. Server-authoritative stamping in Phase 1

In Phase 1 the **server** assigns timestamps (the room clock ticks on each op).
This makes Phase 1 simple and correct: the server is a single sequencer, so the
total order is trivially consistent, and clients can stay thin.

This is *not* the end state. True offline editing (Phase 2) requires the **client**
to hold a CRDT and its own clock, edit locally while disconnected, and merge on
reconnect. The data structures (`LWWMap`, `Clock`, `Merge`, `Snapshot`) are
already designed for that — the same `Merge` runs identically on client and
server. Phase 1 simply doesn't expose the client-side path yet. This staging is
deliberate: ship a correct, demoable system first, then add offline.

## 7. What is intentionally NOT here yet

- **Persistence** — state is in-memory; a server restart clears rooms. (Phase 2)
- **Auth** — `CheckOrigin` allows all origins for local dev. (Phase 3)
- **Rich text** — the LWW-Map handles structured docs; collaborative *text* with
  character-level merging needs a sequence CRDT (RGA/Yjs-style). (Phase 2/stretch)

These are omissions of scope, not of awareness — each is tracked in the roadmap
with the reason it's deferred.
