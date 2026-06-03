# Syncwell Roadmap

Each phase is **independently shippable** — even Phase 1 alone is a complete,
demoable artifact. This is deliberate: the single biggest portfolio mistake is
an unfinished, undeployed project, so the plan never depends on "finishing
everything" to have something to show.

---

## Phase 1 — Shared state + presence ✅ (current)

**Goal:** two browsers edit one document live, conflict-free, with presence.

- [x] LWW-Map CRDT, dependency-free
- [x] Property tests: commutative, associative, idempotent, convergent
- [x] WebSocket sync server, single-owner-goroutine room (lock-free)
- [x] Presence relay (ephemeral)
- [x] Auto-reconnect client SDK + runnable demo
- [x] Single-binary build

**Definition of done:** ✅ `make build` produces one binary; the demo syncs two
windows; tests are green.

---

## Phase 2 — Offline + durability (the depth)

**Goal:** edit while disconnected, reconnect, converge; survive a restart.

- [x] Client-side CRDT: move the clock to the client; edit locally while offline
- [x] `merge` protocol frame (client ships state, server merges) — see PROTOCOL.md
- [x] Persistence: snapshot + op-log to disk (embedded FileStore, `-data` flag)
- [x] Tombstone **garbage collection** via stability low-water mark (DESIGN.md §4)
- [x] Chaos/fuzz test: randomized partitions + concurrent edits must converge
- [x] **Measure & publish:** sync latency p50/p95/p99, throughput, RSS
      (`make bench` → docs/BENCHMARKS.md, numbers in README)
  - [ ] GIF of the demo converging after a partition (Phase 3 polish)

**Why this is the senior signal:** this phase is where the architecture and the
"how it breaks at scale" story (tombstone growth, partition recovery) become
real. Keep `DESIGN.md` updated as decisions land — the doc is as valuable as the
code to a hiring manager.

---

## Phase 3 — Make it real (the #1 hiring signal: real users)

**Goal:** other people run it.

- [x] SDK packaged as `syncwell-client` (+ sdk/README.md); npm-publish-ready
- [x] Auth hooks: room tokens (`-secret`/`-mint`), Origin allow-list, client cap
- [x] Single Docker image (multi-stage, distroless) + one-command run
- [x] Collaborative kanban demo (`demo/kanban.html`)
- [x] CI (GitHub Actions) running the full suite + chaos + bench
- [x] Launch write-up drafted (`docs/BLOG.md`) for Show HN / dev.to
- [ ] **Owner-only steps:** push to GitHub, `npm publish`, deploy hosted demo,
      then track stars / Docker pulls / self-hosting users in the README

---

## Stretch — Horizontal scaling (pure systems flex)

- [ ] Shard rooms across processes/nodes
- [ ] Cross-node fan-out (e.g. a pub/sub backbone) so one hot room can span nodes
- [ ] Benchmark: rooms/node, messages/sec, memory/room

---

## Sequence CRDT (optional, parallel track)

Collaborative *text* with character-level merging needs a sequence CRDT
(RGA / Yjs-style) rather than the LWW-Map. Large effort; only pursue if the
text-editing demo becomes the headline use case.
