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
published.

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

**Acceptance:** each feature ships with a demo, tests, and docs; the SDK is
typed; the README "language-agnostic" claim is demonstrated by a second client.

---

## Phase 7 — Docs, demo, launch

| # | Task | Audit | Size |
| --- | --- | --- | --- |
| 7.1 | Docs site (VitePress): quickstart, API reference, guides, self-hosting | **[F]** | L |
| 7.2 | Hosted demo URL with status page; pin to a released tag | **[F]** | M |
| 7.3 | Protocol spec frozen at v1 with a compatibility policy | **[F]** | S |
| 7.4 | Regenerate README metrics from the live demo; publish the BLOG post | **[F]** | S |
| 7.5 | Roadmap/governance: milestone board, contribution guide refresh | **[F]** | S |

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
- **M2 — Installable:** Phases 3 + 7.1 ✅
- **M3 — Scalable:** Phase 2 ✅
- **M4 — Secure & observable:** Phases 4 + 5 ✅
- **M5 — Feature-complete & launched:** Phases 6 + 7 ✅
