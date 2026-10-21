# Syncwell completion plan — 2026-10-20

**Goal:** take Syncwell from "solid single-node engine with green CI" to **100%
complete**: durable, secure by default, installable, horizontally scalable,
observable, documented, and released.

This plan is the execution follow-up to
[`READINESS_AUDIT_2026-10-19.md`](READINESS_AUDIT_2026-10-19.md). Every task
below is tied to an audit item (`B-*`, `H-*`, `M-*`, `L-*`) or is a new
*feature* (marked **[F]**). Nothing here is done until its **Acceptance** box is
checked and CI proves it.

> **Definition of 100%.** A stranger can `docker run` or `npm i` a tagged
> release; it survives power loss; it runs as two nodes that converge; it is
> safe to expose; its limits are measured; its API is typed and documented; and
> there is a live demo + release artifacts to prove all of it.

Sizes: **S** ≤ 1 day, **M** 2–4 days, **L** 1–2 weeks, **XL** > 2 weeks.

## Progress

- **Phase 0 — complete (2026-10-20).** Browser E2E fails when firefox/geckodriver
  is missing; `make cover-check` enforces per-package coverage floors; CI runs
  `govulncheck`, CodeQL, and Dependabot; wording fixed; issue/PR templates,
  `SECURITY.md`, and `CODE_OF_CONDUCT.md` added.
- **Phase 2 — core complete (2026-10-20).** `-node-id` (required when
  clustering) and `-node-peers`; `/cluster/event` delivers events to local rooms
  (creating them on demand) instead of returning 503; two-node HTTP-relay
  integration test; periodic anti-entropy snapshots heal dropped events; cluster
  metrics + peer-up. Redis/NATS transport (2.3) and explicit node re-homing
  (2.5) remain open; the supported topology is full-mesh replication.
- **Phase 1 — complete (2026-10-20).** `-fsync os|batch|every` (default `batch`,
  5 ms) with a documented contract; truncation-at-every-byte crash test;
  `-backup` / `-verify-backup` / `-restore` with a SHA-256 manifest;
  `make restore-drill` runs in `make all`; `-max-log-bytes` rotation and
  disk-full behavior documented.
- **Phase 3 — complete (2026-10-20).** Hand-written TypeScript declarations
  (generic `Syncwell<State>`, CRDT/sequence types) checked by `tsc` in CI; SDK
  build emits ESM + CJS + minified IIFE with source maps (`npm run build`);
  `Release SDK` publishes `syncwell-client` with provenance on `sdk-v*` tags;
  `Release` runs GoReleaser (3 OS × 2 arch, checksums, SBOM) and pushes a
  multi-arch GHCR image with provenance/SBOM attestations signed keylessly via
  cosign; built-in TLS (`-tls-cert`/`-tls-key`, TLS 1.2+, HTTP/2); Compose,
  Fly, Render, nginx, Caddy, and systemd templates; fixed-parameter benchmark
  workflow. ACME autocert was deliberately left to the proxy/ACME client (no
  account storage in the server).
- **Phase 4 — complete (2026-10-20).** 4.1: public binds without credentials
  auto-generate a 256-bit room secret and admin token and log them once
  (`-insecure` opts out with a warning). 4.2: per-IP token buckets, failure
  lockout, and CIDR allow-lists for `/stats`, `/metrics`, and `/cluster/event`;
  optional `-trust-proxy` for forwarded client IPs. 4.3: `-secret-previous`
  verifies old room secrets during rotation (current secret signs). 4.5:
  fuzz targets for inbound frames, cluster events, token parsing, and canonical
  JSON, plus `docs/THREAT_MODEL.md`. 4.4: OIDC/JWT verification
  (`golang-jwt/jwt/v5`) with discovered/overridden JWKS, `iss`/`aud`/`exp`
  checks, room and capability claims, key refresh/rotation, and fail-closed
  startup behavior.
- **Phase 5 — substantially complete (2026-10-20).** 5.1: fixed-bucket
  Prometheus histograms for register apply latency and inbound/outbound frame
  size (no new metrics dependency). 5.2: optional OTLP/HTTP tracing with
  sampling; spans for HTTP requests, WebSocket joins, CRDT applies, and cluster
  posts. 5.3: importable Grafana dashboard and Prometheus rules. 5.5:
  `docs/SLO.md` with objectives, error budget, and durability semantics. 5.4:
  10k clients / 100 rooms load and soak harness (`cmd/loadtest`), 60 s burst and
  300 s soak published in `docs/LOAD_TEST.md`; run an hours-long soak on the
  target host before making capacity claims.
- **Phase 6 — complete (2026-10-20).** 6.1: `sdk/sequence.js` + `sdk/text.js`
  character-level sequence CRDT wired into `Syncwell`, with a rich-text mark
  API and a collaborative editor demo. 6.2: typed `Awareness` API for identity,
  cursors, and selections, with live cursor overlays. 6.3: grouped undo/redo
  that refuses to clobber newer remote writes. 6.4: `localStorage`, IndexedDB,
  memory, and Node file persistence adapters. 6.5: opt-in state-vector
  `hello`/`delta` sync; clients fall back to snapshots. 6.6: React, Vue, Svelte,
  vanilla, and Node examples. 6.7: authenticated admin dashboard with room list,
  resnapshot, and evict. 6.8: signed, bounded `room.created` /
  `room.persisted` / `room.evicted` webhooks. 6.9: dependency-free Python client
  with a real-server E2E test (`make test-python`).
- **Phase 7 — local work complete (2026-10-20).** 7.1: VitePress site under
  `website/` with quickstart, SDK/API guides, self-hosting, protocol/admin
  reference, project pages, local search, and a GitHub Pages workflow; `make
  docs-build` runs in CI. 7.3: v1 frozen in `docs/PROTOCOL_COMPATIBILITY.md` with
  version/unknown-frame contract tests. 7.5: `docs/MILESTONES.md`,
  `docs/GOVERNANCE.md`, refreshed `CONTRIBUTING.md`, and roadmap links. 7.2/7.4:
  owner runbook in `docs/LAUNCH.md` plus a scheduled demo smoke workflow; the
  live demo/status page, release-tag deployment, npm/GHCR publication, metrics
  refresh, and launch post publication require owner credentials and a released
  tag.

---

## Phase 0 — Guardrails (make "green" mean something)

Cheap, high-leverage. Do this first so later phases cannot silently regress.

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 0.1 | Fail (not skip) browser E2E when firefox/geckodriver missing in CI | M-4 | S |
| 0.2 | Coverage gate in CI (`go test -cover`, min threshold per package) | M-3 | S |
| 0.3 | `govulncheck` + CodeQL + Dependabot | M-3 | S |
| 0.4 | Fix wording: "dependency-free" → "no runtime dependencies"; label bench as indicative | L-1, L-2 | S |
| 0.5 | Add issue/PR templates, `SECURITY.md`, `CODE_OF_CONDUCT.md` | — | S |

**Acceptance:** a PR that drops coverage, adds a vulnerable dep, or breaks the
browser path fails CI. No green run can skip a suite silently.

---

## Phase 1 — Durability & data safety

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 1.1 | Configurable fsync policy: `-fsync=os\|batch\|every` (default `batch`, e.g. ≤5 ms / ≤N writes) | B-3 | M |
| 1.2 | Document the durability contract (what survives `kill -9` vs power loss) in OPERATIONS/DESIGN | B-3, L-3 | S |
| 1.3 | Fault-injection test: kill at arbitrary write offsets; assert no torn/partial state after restart | B-3 | M |
| 1.4 | Backup automation: `syncwell -backup DIR` (consistent snapshot + checksum) + restore verifier | H-4 | M |
| 1.5 | Wire `test/restore_drill.mjs` into `make all`/CI | H-4 | S |
| 1.6 | Log rotation/size cap for room logs; disk-full behavior documented | **[F]** | M |

**Acceptance:** with `-fsync=every`, a simulated power cut loses nothing;
backup→restore round-trips in CI; durability guarantees are written down.

---

## Phase 2 — Horizontal scaling (make the cluster real)

Turns the inert cluster code into a working multi-node system.

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 2.1 | `-node-id` (required when clustering) + `-node-peers`; reject duplicate/empty ids | B-2 | S |
| 2.2 | Wire a real `ClusterBus`; make `/cluster/event` apply relayed events instead of 503 | B-1 | L |
| 2.3 | Optional shared transport backend (Redis pub/sub **or** NATS) behind a build flag/plugin | **[F]** | L |
| 2.4 | Two-node integration test through `HTTPClusterRelay` → `ServeClusterEvent` → convergence | B-1, M-5 | M |
| 2.5 | Room ownership/sharding via `ShardPlanner`; document routing + failover | B-2 | M |
| 2.6 | Anti-entropy: periodic state-digest exchange so *dropped* relay events still converge | **[F]** | L |
| 2.7 | Cluster metrics (`cluster_lag`, `relay_dropped_total`, `peer_up`) + alerts | **[F]** | M |

**Acceptance:** two processes with distinct `-node-id` converge under partition
and message loss; a killed node's rooms are re-homed; docs explain the topology.

---

## Phase 3 — Packaging, releases, SDK

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 3.1 | TypeScript declarations for the SDK (`syncwell.d.ts`, generics for state types) | **[F]** | M |
| 3.2 | SDK build: ESM + CJS + minified browser bundle + source maps | **[F]** | M |
| 3.3 | npm publish workflow (provenance, `--access public`), keep `syncwell-client` | H-1 | S |
| 3.4 | GoReleaser: multi-arch binaries, checksums, SBOM; tag → GitHub release | H-1 | M |
| 3.5 | Publish container to GHCR (multi-arch, signed) | H-1 | S |
| 3.6 | Built-in TLS: `-tls-cert`/`-tls-key` (+ optional ACME autocert) | H-2 | M |
| 3.7 | Deploy templates: `fly.toml`, `render.yaml`, `docker-compose.yml`, nginx/caddy samples | H-2 | M |
| 3.8 | Reproducible benchmark in CI; regenerate `docs/BENCHMARKS.md` | M-1, L-2 | S |

**Acceptance:** `npm i syncwell-client`, `docker run ghcr.io/…`, and a downloaded
`vX.Y.Z` binary all work from a clean machine; TLS can be enabled without a proxy.
Satisfied by the workflows and templates above once the first tags are pushed;
ACME issuance is delegated to a proxy or ACME client.

---

## Phase 4 — Security hardening

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 4.1 | Safe-by-default: refuse to bind non-loopback without `-secret`/`-admin-token`, or auto-generate + print once | H-3 | S |
| 4.2 | Rate limiting + lockout on `/stats`, `/metrics`, `/cluster/event`; optional IP allowlist | M-6 | M |
| 4.3 | Key rotation: accept a set of secrets (current + previous) for zero-downtime rotation | **[F]** | M |
| 4.4 | OIDC/JWT integration path (verify external IdP tokens → capability claims) | **[F]** | L |
| 4.5 | Threat model doc + fuzzing of frame/JSON parsers | **[F]** | M |

**Acceptance:** a public bind with no auth is impossible (or loudly unsafe);
admin surfaces resist brute force; `govulncheck`/CodeQL clean; threat model
published. All Phase 4 items are implemented; OIDC requires provider-side
claims (`syncwell_room`, `syncwell_caps`) as documented in `docs/DEPLOY.md`.


---

## Phase 5 — Observability & scale proof

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 5.1 | Prometheus histograms (op latency, frame size), not just counters | **[F]** | M |
| 5.2 | OpenTelemetry traces (optional exporter) | **[F]** | M |
| 5.3 | Grafana dashboard JSON; ship alert rules | **[F]** | S |
| 5.4 | Load + soak test: ≥10k clients across rooms, hours-long run, published report | M-1 | L |
| 5.5 | SLO/SLA doc (latency, availability, memory ceilings) | **[F]** | S |

**Acceptance:** a published load report with p50/p95/p99 and RSS; dashboards +
alerts import cleanly; SLOs are stated.

---

## Phase 6 — Product features (what makes it "complete" for users)

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 6.1 | Sequence/rich-text CRDT wired into SDK + a demo (not just `internal/sequence`) | M-2 | L |
| 6.2 | Typed awareness API (cursors, selections, user identity) + demo cursors | **[F]** | M |
| 6.3 | Undo/redo (per-client history over the CRDT) | **[F]** | M |
| 6.4 | SDK persistence adapters: IndexedDB + `localStorage` + Node file | **[F]** | M |
| 6.5 | Delta/state-vector sync (send diffs, not full snapshots) | **[F]** | L |
| 6.6 | Framework examples: React hook, Vue composable, Svelte store, vanilla, Node server | **[F]** | L |
| 6.7 | Admin dashboard UI (rooms, clients, GC watermark, force-resnapshot/evict) | **[F]** | L |
| 6.8 | Webhooks/events on room milestones (created, persisted, evicted) | **[F]** | M |
| 6.9 | Polyglot client (Python or Rust) proving "language-agnostic" | **[F]** | XL |

**Acceptance (2026-10-20):**
- [x] Each feature ships with code, tests, and docs.
- [x] The SDK is typed (`npm run typecheck` compiles the declarations).
- [x] `demo/sequence.html` demonstrates sequence editing, cursors, and undo/redo.
- [x] The admin dashboard and webhook endpoints are covered by Go tests.
- [x] A second client language is demonstrated: `python/syncwell_client.py` +
      `python/test_client_e2e.py` (`make test-python`).
- [x] Delta sync is documented in `docs/PROTOCOL.md`; operator surfaces in
      `docs/ADMIN_AND_WEBHOOKS.md`.

---

## Phase 7 — Docs, demo, launch

| # | Task | Audit | Size | State |
| --- | --- | --- | --- | --- |
| 7.1 | Docs site (VitePress): quickstart, API reference, guides, self-hosting | **[F]** | L | ✅ build + Pages workflow; hosting owner-only |
| 7.2 | Hosted demo URL with status page; pin to a released tag | **[F]** | M | 🟡 runbook + smoke workflow; deploy owner-only |
| 7.3 | Protocol spec frozen at v1 with a compatibility policy | **[F]** | S | ✅ frozen + contract tests |
| 7.4 | Regenerate README metrics from the live demo; publish the BLOG post | **[F]** | S | 🟡 post refreshed; live metrics/publish owner-only |
| 7.5 | Roadmap/governance: milestone board, contribution guide refresh | **[F]** | S | ✅ complete |

**Acceptance:** docs site live, demo live on a released tag, launch post
published, and every "still ahead" line in the README resolved.

---

## Suggested order & dependencies

```
Phase 0 ──▶ Phase 1 ──┐
                      ├─▶ Phase 2 ──▶ Phase 5 ──▶ Phase 7
        Phase 3 ──────┘                 ▲
        Phase 4 ────────────────────────┘
        Phase 6 ──(needs 3.1/3.2)───────┘
```

- **0** first (guardrails), then **1** (data safety) and **3** (packaging) in
  parallel — these deliver the biggest perceived jump ("installable + durable").
- **2** (cluster) is the largest engineering risk; start the design early.
- **4** is mostly independent; **6** depends on the SDK build from **3.1/3.2**.
- **7** last, once **3** and **6** give it something to show.

## Risks / decisions to make

1. **Cluster transport:** Redis vs NATS vs custom HTTP bus. Affects 2.2–2.6.
2. **Durability default:** `batch` (fast, tiny loss window) vs `every` (safe,
   slower). Affects 1.1 and the marketing claim.
3. **Second client language:** Python (easiest) vs Rust (perf story). Affects 6.9.
4. **Build strategy for SDK:** hand-written types + esbuild vs tsc. Affects 3.1/3.2.
5. **Hosted demo host:** Fly/Railway/Koyeb (see `docs/DEPLOY.md`). Affects 7.2.

## Milestones (suggested)

- **M1 — Trustworthy build:** Phases 0 + 1 ✅
- **M2 — Installable:** Phase 3 ✅; docs site code/build ✅; release tags and docs
  hosting are owner-only
- **M3 — Scalable:** Phase 2 ✅ (full-mesh HTTP relay; Redis/NATS remains stretch)
- **M4 — Secure & observable:** Phases 4 + 5 ✅
- **M5 — Feature-complete:** Phase 6 ✅
- **M6 — Launched:** Phase 7 local work ✅; hosted demo, owner deployment, and
  publication remain

The live board and release gates are in [`MILESTONES.md`](./MILESTONES.md).
