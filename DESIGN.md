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

**Fix (Phase 2.3 — shipped):** **stability-based garbage collection.** The
server tracks, per connected client, the highest logical clock counter that
client has revealed (via its writes and a periodic `ack` heartbeat). The
**low-water mark** is the minimum of those counters. Because Lamport counters
only increase, no future write from a connected client can fall below the
low-water mark — so tombstones with a counter strictly below it are *causally
stable* and safe to delete. GC runs during each maintenance pass
(`Room.maintain`), and `/stats` exposes live/tombstone counts and the current
low-water mark. The `make gc` test churns 40 deletes and shows tombstones
collapse to ~1.

**Known limit (stated honestly):** the low-water mark only accounts for
*connected* clients. A client offline longer than a GC cycle is not counted, so
if it later reconnects with stale offline edits to a key whose tombstone was
already collected, that key could resurrect. Mitigations for a production
deployment: bound maximum client offline time, or persist per-client version
vectors so disconnected replicas still hold back the low-water mark. We keep the
connected-replica bound here because it is simple, correct for the common case,
and the trade-off is explicit rather than hidden.

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

## 6. Client-authoritative timestamps (Phase 2 — shipped)

Phase 1 was server-authoritative: the room clock stamped each write. That was a
simple, correct starting point but it cannot support offline editing — a
disconnected client has no server to stamp it.

Phase 2 moves authority to the **client**. Each client holds its own `LWWMap`
replica and Lamport `Clock` (`sdk/crdt.js`, a faithful mirror of the Go code).
Writes:

1. apply to the local replica **first** (optimistic — the UI updates with no
   round-trip and keeps working offline), then
2. are sent to the server as an `op` carrying the client-stamped register.

The server becomes a merge-and-fan-out relay: it folds each incoming register
into the room CRDT and broadcasts the winner. On (re)connect a client ships its
full local state via a `merge` frame, so edits made while offline propagate, and
receives a snapshot back so it converges with everything it missed.

Because the *same* merge runs on both sides and is idempotent/commutative,
reconnection needs no replay log or ack protocol — re-merging a snapshot cannot
corrupt state. The end-to-end test in `test/offline_e2e.mjs` cuts a client's
socket, edits on both sides, reconnects, and asserts byte-for-byte convergence.

**Consequence worth stating plainly:** "last writer wins" is by *logical* clock,
so a client that made several edits while offline can have a higher counter than
a concurrent online edit and win the conflict — "last" is last in causal order,
not wall-clock time (§3). For a collaboration tool this is the right trade-off;
no edit is silently lost, and the outcome is deterministic across all replicas.

## 7. Auth: stateless room tokens (Phase 3 — shipped)

Access control is opt-in and database-free. When a server is started with a
`-secret`, each room requires a token: `"<exp>.<hmac-sha256(secret, room|exp)>"`
(`internal/server/auth.go`). The server verifies with the shared secret alone —
no token store, no per-request DB hit — and the check is constant-time on the
signature. Tokens are room-scoped and expiring; `syncwell -secret S -mint room`
issues one. With no secret set, auth is disabled (open dev mode). Two more
guards layer on top: an **Origin allow-list** (`-origins`) and a **per-room
client cap** (`-max-clients`). This keeps the common self-host case
zero-config while making a locked-down deployment one flag away.

## 8. What is intentionally NOT here yet

- **Rich text** — the LWW-Map handles structured docs; collaborative *text* with
  character-level merging needs a sequence CRDT (RGA/Yjs-style). (Stretch)
- **Horizontal scaling** — one room is owned by one process today; sharding
  rooms across nodes is the scaling stretch goal (§5, ROADMAP).
- **fsync-per-write durability** — writes flush to the OS (survive process
  kill); surviving power loss would add an fsync knob (§4 / store).

These are omissions of scope, not of awareness — each is tracked in the roadmap
with the reason it's deferred.
