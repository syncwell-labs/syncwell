# Building a self-hostable real-time collaboration engine in Go

*(Launch draft for Show HN / dev.to. Replace the repository/docs/demo links with
the published URLs, regenerate the numbers on the release hardware, then post.)*

Every product eventually wants multiplayer: live cursors, presence, shared state
that works when two people edit at once. The good hosted options are closed and
per-seat priced. I wanted to understand the hard parts myself and end up with
something I could self-host, so I built **Syncwell** — an MIT-licensed
collaboration engine that ships as a single Go binary.

This post is about the problems that actually make real-time collaboration hard,
the design decisions behind each, and the parts that are still honest
limitations.

## 1. Merging concurrent edits without conflicts

Two users edit the same field at the same time. Who wins, and how do all replicas
agree? The mainstream answers are **Operational Transform** (classic Google Docs)
and **CRDTs**.

I chose a **CRDT** for one decisive reason: testability. Correctness reduces to
three algebraic laws — merge must be commutative, associative, and idempotent:

```text
merge(a, b) == merge(b, a)              // delivery order doesn't matter
merge(merge(a, b), c) == merge(a, merge(b, c))
merge(a, a) == a                        // duplicate delivery is harmless
```

OT's correctness lives in transform functions that are famously easy to get
subtly wrong. Syncwell starts with a state-based last-writer-wins map: each key
holds a value, a logical timestamp, and a tombstone flag. A duplicated or
reordered message cannot corrupt state because every apply is an idempotent CRDT
join.

Character-level text needs more than a map, so the SDK also has a rank-ordered
sequence CRDT. Each character gets a fractional rank; an insert between two
characters sits between their ranks, so concurrent typing at the same position
interleaves deterministically instead of colliding on an array index.

## 2. "Last writer wins" — but whose clock?

Wall-clock time invites clock-skew bugs: the replica with the fast clock wins
everything. Syncwell uses **Lamport logical clocks** plus a node id for a total
order, without synchronized clocks.

The honest trade-off: "last" means *last in causal order*, not last in real
time. A client that edited several times offline can have a higher logical
counter than a concurrent online edit and win. For collaboration that's the right
call — no edit is silently lost, and every replica resolves the conflict
identically.

The client authors its own timestamps. The server is a merge-and-fan-out relay,
not a sequencer. That single decision is what makes offline editing possible.

## 3. Offline editing and reconnect

The client holds its own replica. Writes apply locally first (instant UI, works
with no network), then sync. On reconnect the client sends its state vector and
any offline writes. The server merges those writes and returns only the registers
the client missed.

Because merge is idempotent and commutative, **no replay log or acknowledgement
protocol is needed**. Delta sync is an optimization, not a correctness
requirement: if the server is older or the room was compacted past the client's
vector, both sides fall back to a full snapshot + merge.

I proved this end-to-end with a test that cuts a socket, edits on both sides,
reconnects, and asserts byte-for-byte convergence. A chaos test runs five
replicas through 3,000 randomized operations with partitions, reordering, and
duplicate delivery; they always converge.

## 4. The parts nobody mentions

- **Memory.** Tombstones can't be removed immediately: a delete must be able to
  beat a concurrent stale write. A long-lived document would grow forever unless
  you garbage-collect. Syncwell tracks a causal-stability low-water mark and
  collects tombstones below it. A test collapses 40 deletes to roughly one
  tombstone.
- **Concurrency.** Each room is owned by a single goroutine — the only code that
  touches the document. No mutexes, no data races by construction, just message
  passing.
- **Durability.** An append-only log per room, tolerant of a torn final record,
  compacted periodically. It survives `kill -9`; `-fsync every` closes the
  power-loss window. Backup/restore uses a SHA-256 manifest and a restore drill
  runs in CI.
- **Presence.** Cursors and selections are ephemeral: never persisted, never
  replayed, with heartbeats and stale-peer pruning. The first presence update is
  queued until the socket has a snapshot, so an initial cursor is not lost during
  the handshake.
- **Operations.** Prometheus metrics, optional OTLP tracing, SLOs, a Grafana
  dashboard, an authenticated admin dashboard (resnapshot/evict), and signed,
  bounded room-lifecycle webhooks.

## 5. What is deliberately not done

Syncwell is not a multi-region database, a hosted SaaS, or a full block-based
rich-text editor. Marks are per character. The supported cluster topology is a
full-mesh HTTP relay, not a Redis/NATS backbone. Horizontal scaling beyond that
is roadmap work, and the load-test numbers are indicative, not an SLA.

That scope is deliberate: a small, correct, self-hostable engine is more useful
than an unfinished platform.

## Numbers

On one box with 50 concurrent clients in one room, the benchmark reports roughly
**p50 1.3 ms / p99 3.6 ms** sync latency, **~9,000 messages/sec**, and **11.5 MB**
RSS. Reproduce it with `make bench`; run your own soak on your hardware before
making capacity claims.

## Try it

```bash
go run ./cmd/syncwell
# open http://localhost:8080/sequence.html in two windows
```

Install the SDK with `npm install syncwell-client`, or read the
[self-hosting guide](https://github.com/Nil-Vaghani/syncwell/blob/main/docs/DEPLOY.md).

Code, design notes, and the test suite that proves all of the above:
**https://github.com/Nil-Vaghani/syncwell**.

Feedback is most welcome on the GC low-water-mark approach, the offline conflict
semantics, and the sequence-CRDT interleaving rules.
