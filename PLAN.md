# Syncwell — Completion Plan (to 100%)

This is the single source of truth for finishing Syncwell. It defines what
"100% complete" means, breaks the remaining work into ordered, independently
shippable slices, and gives each slice a concrete **Definition of Done (DoD)**
so progress is unambiguous. Checkboxes are updated as work lands.

> **North star:** the two hiring signals that survived the research were
> *(1) real users + measurable impact* and *(2) demonstrated architecture &
> justified trade-offs*. Every slice below exists to produce one or both. We do
> not add scope that serves neither.

---

## Definition of "100% complete"

Syncwell is done when ALL of the following are true:

1. **Offline-capable:** a client can edit while disconnected and converge on
   reconnect, with no lost edits and no manual conflict resolution.
2. **Durable:** the server survives a restart with zero document loss.
3. **Bounded memory:** tombstones are garbage-collected; a long-lived room does
   not grow without bound.
4. **Proven correct:** a chaos/fuzz suite randomizes partitions, duplication,
   and concurrent edits and asserts convergence — green in CI.
5. **Measured:** the README shows real numbers (sync latency p99 vs. concurrent
   clients; convergence-after-partition; memory/room) produced by a committed
   benchmark, not hand-waved.
6. **Usable by others:** published client SDK, auth on rooms, one-command Docker
   deploy, a hosted live demo URL.
7. **Demoable:** a visually compelling collaborative app (kanban) that a
   non-engineer immediately understands.
8. **Documented & CI'd:** README, DESIGN, PROTOCOL, ROADMAP current; GitHub
   Actions runs vet + race tests + benchmark on every push; badges green.

When 1–8 hold, the project is launch-ready (Show HN / r/programming) and the
remaining work is *distribution*, not engineering.

---

## Status legend
`[ ]` not started `[~]` in progress `[x]` done

---

## Phase 1 — Shared state + presence  `[x]` DONE
Already shipped and committed. Listed for completeness.
- [x] LWW-Map CvRDT, zero deps
- [x] Property tests: commutative / associative / idempotent / convergent
- [x] WebSocket server, single-owner-goroutine room (lock-free)
- [x] Presence relay; auto-reconnect SDK; runnable demo; single binary

---

## Phase 2 — Offline + durability (the depth + the numbers)

### 2.1 Client-side CRDT & offline editing  `[x]` DONE
Move authority for timestamps to the client so edits work offline and merge on
reconnect. The same `LWWMap`/`Clock`/`Merge` runs on both sides.

- [x] Port the CRDT to the SDK in JS (`sdk/crdt.js`): `LWWMap`, `Clock`, `merge`,
      `snapshot` — mirror of the Go logic, with unit tests (`sdk/crdt.test.js`).
- [x] SDK holds a local document; `set`/`delete` apply locally first (optimistic).
      UI reads from the local doc via the `change` event, not the wire.
- [x] New protocol frame `merge` (client→server) carrying client register state;
      server merges into the room doc and re-broadcasts winners + a snapshot.
      Client-authored `op` carries the stamped register.
- [x] Offline path: while the socket is down, edits accumulate in the local CRDT;
      on reconnect the client ships full state (`merge`) and pulls a snapshot.
- [x] Clock authority moved client-side; server merges (no longer the sole
      sequencer). DESIGN.md §6 + PROTOCOL.md updated.

**DoD:** ✅ `make e2e` cuts a client's socket, edits on both sides, reconnects,
and asserts byte-for-byte convergence with no lost edits.

### 2.2 Persistence  `[x]` DONE
Survive a restart.

- [x] `internal/store` interface: `Load`, `Append`, `Compact`, `Close`.
- [x] `FileStore`: append-only op-log per room + atomic snapshot compaction;
      tolerates torn final line from a crash. No external DB (single-binary).
- [x] Room loads state on first access; winning writes appended; compaction
      every 256 writes.
- [x] Wired into the hub; server flag `-data <dir>` (empty = in-memory);
      graceful shutdown flushes the store.

**DoD:** ✅ `make persist` writes through a client, SIGKILLs the server, restarts
against the same data dir, and a fresh client receives the persisted document.
Unit tests cover load-after-restart, compaction, and torn-line tolerance.

### 2.3 Tombstone garbage collection  `[x]` DONE
Bound memory (DESIGN.md §4).

- [x] Per-client clock tracking + `ack` heartbeat frame; cluster low-water mark
      = min across connected clients.
- [x] Maintenance pass collects tombstones strictly below the low-water mark;
      runs in-memory and persistent. Honest limit (offline clients) documented.
- [x] `/stats` endpoint exposes per-room live/tombstone counts + low-water mark.

**DoD:** ✅ `make gc` churns 40 deletes and asserts tombstones collapse (to ~1)
with the live key intact; unit test covers the collection predicate.

### 2.4 Chaos/convergence test + benchmark (the headline numbers)  `[x]` DONE
- [x] Chaos test (Go): 5 replicas × 3000 ops, randomized set/delete on
      overlapping keys, partition + shuffled + duplicated heal; asserts
      byte-identical convergence to a reference. Seeded (5 seeds) → reproducible.
- [x] Benchmark harness (`test/bench.mjs`): K concurrent clients, fixed publish
      rate, measures p50/p95/p99 sync latency, throughput, and server RSS;
      writes `docs/BENCHMARKS.md`.
- [x] Real numbers in README (p50 1.3 / p99 3.6 ms, ~9k msg/s, 11.5 MB @ 50
      clients). _GIF capture deferred to Phase 3 demo polish._

**DoD:** ✅ `make chaos` and `make bench` exist and pass/produce numbers; README
shows real figures.

---

## Phase 3 — Make it real (distribution + polish)

### 3.1 Auth & hardening  `[x]` DONE
- [x] Room access tokens (HMAC, expiring, room-scoped); `-secret` enables auth,
      `-mint` issues tokens. Origin allow-list via `-origins`.
- [x] Message-size cap (1 MiB) + per-room client cap (`-max-clients`).
- [x] Graceful shutdown (drain connections, flush persistence) — Phase 2.2.

### 3.2 Packaging & deploy  `[ ]`
- [ ] Publish the client SDK as an npm package (`syncwell-client`).
- [ ] Finalize Docker image; `docker run` one-liner in README.
- [ ] Deploy a hosted live demo; put the URL in README. (Fly.io/Render/VPS —
      single static binary makes this trivial.)
- [ ] GitHub Actions: vet + `go test -race` + chaos + bench on push; badges.

### 3.3 Flagship demo  `[x]` DONE
- [x] Collaborative **kanban board** (`demo/kanban.html`): drag cards between
      columns, add/delete, presence avatars — all synced live on the SDK.
- [x] Simple text demo kept as the "hello world", cross-linked.

### 3.4 Docs & launch prep  `[ ]`
- [ ] README: final positioning, real numbers, GIF, quickstart, deploy.
- [ ] DESIGN.md: bring §6 and §4 up to date with what shipped.
- [ ] A short write-up of the CRDT design (the basis of the Show HN / dev.to post).
- [ ] CONTRIBUTING.md + issue templates (signals an inviting OSS project).

---

## Stretch (only if the above is fully done)
- [ ] Horizontal scaling: shard rooms across nodes via a pub/sub backbone.
- [ ] Sequence CRDT (RGA/Yjs-style) for character-level collaborative text.

---

## Execution order (what I build, in sequence)
1. **2.1 client-side CRDT + offline** ← start here (the core depth)
2. **2.2 persistence** (needed before GC is meaningful)
3. **2.3 tombstone GC**
4. **2.4 chaos test + benchmark** (turns the above into headline numbers)
5. **3.3 kanban demo** (make the depth visible)
6. **3.1 auth + 3.2 deploy + CI** (ship it)
7. **3.4 docs + launch prep**

Each step ends green (vet + race tests) and is committed before the next begins.
