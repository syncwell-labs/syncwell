# Syncwell Roadmap

Each phase is **independently shippable** — even Phase 1 alone is a complete,
demoable artifact. This is deliberate: the single biggest portfolio mistake is
an unfinished, undeployed project, so the plan never depends on "finishing
everything" to have something to show.

---

## Phase 1 — Shared state + presence ✅

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

## Phase 2 — Offline + durability ✅

**Goal:** edit while disconnected, reconnect, converge; survive a restart.

- [x] Client-side CRDT: move the clock to the client; edit locally while offline
- [x] `merge` protocol frame (client ships state, server merges) — see PROTOCOL.md
- [x] Persistence: snapshot + op-log to disk (embedded FileStore, `-data` flag)
- [x] Tombstone **garbage collection** via stability low-water mark (DESIGN.md §4)
- [x] Chaos/fuzz test: randomized partitions + concurrent edits must converge
- [x] **Measure & publish:** sync latency p50/p95/p99, throughput, RSS
      (`make bench` → docs/product/BENCHMARKS.md, numbers in README)
  - [ ] GIF of the demo converging after a partition (Phase 3 polish)

**Why this is the senior signal:** this phase is where the architecture and the
"how it breaks at scale" story (tombstone growth, partition recovery) become
real. Keep `docs/architecture/DESIGN.md` updated as decisions land — the doc is as valuable as the
code to a hiring manager.

---

## Phase 3 — Make it real (the #1 hiring signal: real users) ✅

**Goal:** other people run it.

- [x] SDK packaged as `syncwell-client` with TypeScript declarations, ESM/CJS/
      browser builds (+ clients/javascript/README.md); npm-publish-ready
- [x] Release automation: GoReleaser archives + checksums + SBOMs, npm
      provenance workflow, multi-arch signed GHCR image
- [x] Built-in TLS (`-tls-cert`/`-tls-key`) + Compose/Fly/Render/nginx/Caddy/
      systemd templates
- [x] Auth hooks: room tokens (`-secret`/`-mint`), Origin allow-list, client cap
- [x] Single Docker image (multi-stage, distroless) + one-command run
- [x] Collaborative kanban demo (`examples/demos/kanban.html`)
- [x] CI (GitHub Actions) running the full suite + chaos + bench
- [x] Launch write-up drafted (`docs/product/BLOG.md`) for Show HN / dev.to
- [ ] **Owner-only steps:** push to GitHub, `npm publish`, deploy hosted demo,
      then track stars / Docker pulls / self-hosting users in the README

---

## Phase 4 — Safe to expose ✅

- [x] Safe-by-default public binds (generated room secret + admin token) and
      `-insecure` opt-out
- [x] Per-IP admin limits, failure lockout, CIDR allow-lists, `-trust-proxy`
- [x] Secret rotation (`-secret-previous`), capability tokens, OIDC/JWT
- [x] Fuzz targets, threat model, `govulncheck`/CodeQL, Dependabot

## Phase 5 — Observable and measured ✅

- [x] Prometheus histograms (apply latency, frame sizes) and cluster/webhook
      counters
- [x] Optional OTLP/HTTP tracing with spans for HTTP, joins, applies, cluster
- [x] Grafana dashboard, alert rules, SLOs, and a 10k-client load/soak harness

## Phase 6 — Product features ✅

- [x] Sequence/rich-text CRDT (`SequenceText`) and collaborative text demo
- [x] Typed awareness (presence, cursors, selections) with live cursor overlays
- [x] Grouped undo/redo that respects newer remote writes
- [x] Persistence adapters: `localStorage`, IndexedDB, memory, Node file
- [x] Opt-in state-vector delta sync on reconnect
- [x] React, Vue, Svelte, vanilla, and Node examples
- [x] Admin dashboard (rooms, resnapshot, evict) and signed room webhooks
- [x] Dependency-free Python client with a real-server E2E test

---

## Phase 7 — Launch polish (in progress)

- [x] Docs site (VitePress): quickstart, API reference, guides, self-hosting;
      builds in CI and deploys via GitHub Pages (owner enables Pages)
- [x] Frozen protocol v1 compatibility policy (`docs/protocol/PROTOCOL_COMPATIBILITY.md`)
      with contract tests
- [x] Milestone board (`docs/product/MILESTONES.md`) and refreshed contribution guide
- [x] Hosted-demo/status runbook (`docs/product/LAUNCH.md`) and scheduled demo smoke
- [ ] Publish docs URL (owner-only: enable Pages and run the Docs workflow)
- [ ] Hosted demo URL with status page, pinned to a released tag (owner-only)
- [ ] Publish the refreshed launch post and regenerated metrics (owner-only)

---

Milestones and owner-only release gates are tracked in
[`MILESTONES.md`](./MILESTONES.md). Protocol changes follow
[`PROTOCOL_COMPATIBILITY.md`](../protocol/PROTOCOL_COMPATIBILITY.md).

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

The sequence CRDT is now wired into the product. `clients/javascript/sequence.js` provides the
rank-ordered RGA core; `clients/javascript/text.js` (`SequenceText`) adds offset operations,
rich-text marks, and undo-friendly batches; and `examples/demos/sequence.html` is a live
collaborative editor with remote cursors and a formatted preview. Text edits
travel over the same room, persistence, delta-sync, and awareness paths as the
LWW map, so offline editing and reconnect work unchanged.

For horizontal scaling, the repo now also has a rendezvous shard planner and a
minimal in-process cluster bus. That gives us a concrete room-placement and
event-fanout primitive, but it is still a foundation rather than a full
multi-process deployment.

The remaining gap is external cluster membership and a production-ready
multi-node control plane for those room events; the bus is still best thought
of as a local development and test primitive. The wire codec and HTTP ingress
/ egress shape now exist, so the next step can focus on the cluster plumbing
rather than the event model itself.
