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
- [ ] Tombstone **garbage collection** via stability low-water mark (DESIGN.md §4)
- [ ] Chaos/fuzz test: randomized partitions + concurrent edits must converge
- [ ] **Measure & publish:** sync latency p99 vs. concurrent-client count;
      convergence-after-partition proof (record a GIF for the README)

**Why this is the senior signal:** this phase is where the architecture and the
"how it breaks at scale" story (tombstone growth, partition recovery) become
real. Keep `DESIGN.md` updated as decisions land — the doc is as valuable as the
code to a hiring manager.

---

## Phase 3 — Make it real (the #1 hiring signal: real users)

**Goal:** other people run it.

- [ ] Polished SDK + docs site; npm package for the client
- [ ] Auth hooks (room tokens; lock down `CheckOrigin`)
- [ ] Single Docker image + one-command deploy; hosted live demo URL
- [ ] A second, visually compelling demo (collaborative kanban or text editor)
- [ ] Launch: "Show HN", r/programming, dev.to writeup of the CRDT design
- [ ] **Track real numbers:** GitHub stars, Docker pulls, self-hosting users —
      put them at the top of the README

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
