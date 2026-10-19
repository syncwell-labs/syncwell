# Syncwell readiness audit — 2026-10-19

**Verdict: NOT production-ready.** The single-node engine is genuinely solid and
well-tested. Everything that makes it a *deployable, installable, scalable
product* — packaging, power-loss durability, and multi-node operation — is
either missing or non-functional in the shipped binary.

- **Commit audited:** `d042169` (`master`)
- **Method:** static read of the tree, `go test ./... -cover`, CI logs, and live
  probes of the HTTP endpoints. No load, soak, or penetration testing was run;
  see [Audit limitations](#audit-limitations).

This document is deliberately adversarial. It exists because the issue list in
[`PROJECT_SCAN_ISSUES_2026-10-19.md`](PROJECT_SCAN_ISSUES_2026-10-19.md) is
*complete* (all B/H/M/L items fixed, CI green) and it is easy to read that as
"the project is ready". It is not. "Scan complete" ≠ "shippable".

---

## What is actually ready

| Area | Evidence |
| --- | --- |
| CRDT core (Go + JS), RFC 8785 JCS convergence | `internal/crdt` 76% cover; 15 shared Go↔JS fixtures; `TestChaosConvergence` |
| WebSocket sync, single-owner room goroutine, presence relay | `internal/server` 63% cover; offline/persist/GC/multi-client E2E green |
| Persistence + tombstone GC + restart across `SIGKILL` | `internal/store` 76% cover; `make persist`, `make gc` |
| Token auth with capabilities, revocation, resource limits, rate limits | `internal/server/access*.go`, `server.go`; `-secret`, `-mint*`, `Limits` |
| Observability + lifecycle | `/healthz`, `/readyz`, `/stats`, `/metrics`, graceful drain (`main.go`) |
| Build hygiene | single static binary, `make all` green, CI green on `master` |

Statement coverage (measured, this audit):

```
internal/crdt       76.0%
internal/store      76.3%
internal/server     63.4%
internal/sequence   62.3%
cmd/syncwell         0.0%
internal/version     0.0%
```

---

## Blockers

These block any honest "production" or "multi-node" claim.

### B-1 — The cluster relay cannot receive events
- `cmd/syncwell/main.go:105` builds `server.Config` but **never sets `Bus`**.
- `internal/server/server.go:138` returns **503 "cluster bus unavailable"** when
  `h.bus == nil`.
- Consequence: `-cluster-peers` makes each node POST events to its peers'
  `/cluster/event`, and every receiving node **rejects them with 503**. The only
  code path that injects a real bus is the test suite
  (`internal/server/bus_test.go`, `MemoryClusterBus`).
- **Done means:** the binary wires a bus (in-process + HTTP) so relayed events
  are actually applied, with an end-to-end test through `HTTPClusterRelay`.

### B-2 — No node identity, so nodes are mutually invisible
- `main.go` has **no `-node-id` flag** and never sets `Config.NodeID`.
- `internal/server/hub.go:676` defaults `nodeID = "local"`.
- `internal/server/server.go` drops cluster messages where `From == r.nodeID`,
  so two default-configured nodes both named `"local"` treat each other's events
  as their own and discard them.
- `Config.Peers` is also never set from the CLI, so
  `ShardPlanner.Owner()` (`internal/server/shard.go:46`) always returns the
  local node — placement/sharding is inert outside tests.
- **Done means:** a required, unique `-node-id`, plus `-node-peers`, with an
  integration test across two hubs.

### B-3 — No power-loss durability
- `internal/store/store.go:173` `Append` calls `w.Flush()` only — data reaches
  the OS page cache, not the disk. The code comment (line 172) admits this:
  *"fsync for power-loss durability is a future knob"*.
- Only compaction (`writeLog`, `store.go:239`) fsyncs.
- Consequence: "durable" here means **survives process kill (`SIGKILL`)** — which
  the tests prove — but **not** machine crash / power loss. Those are different
  guarantees.
- **Done means:** a configurable fsync/per-write or batched-fsync policy, a
  documented durability contract, and a crash-simulation test.

---

## High

### H-1 — Nothing is published
- **No git tags and no GitHub releases** (`git tag` empty; `gh release list` empty).
- **npm package is not published**: `syncwell-client` → `404` (version `0.2.0`
  in `sdk/package.json`).
- No container image published to any registry.
- A user cannot `npm install`, `docker pull`, or download a versioned binary.

### H-2 — No in-process TLS
- The server is plain HTTP; TLS termination is delegated to a reverse proxy
  (`docs/OPERATIONS.md`). Acceptable for self-host, but it is an undeclared
  deployment prerequisite and there is no sample proxy config in-repo.

### H-3 — Insecure-by-default auth
- `main.go:29` — empty `-secret` **disables room-token auth entirely**.
- `main.go:30` — empty `-admin-token` leaves `/stats` and `/metrics` **open**.
- Safe on `localhost`; dangerous the moment the binary is bound to a public
  interface, which is exactly what a naive deployment does.
- **Done means:** refuse to start (or loudly warn) when auth is unset on a
  non-loopback `-addr`, and document the required env vars.

### H-4 — Backup/restore is manual
- `docs/RESTORE_DRILL.md` is a human checklist and `test/restore_drill.mjs` is
  not wired into `make all` or CI. There is no scheduled backup automation and
  no verification that a backup is restorable.

---

## Medium

### M-1 — Scale evidence is thin
- `test/bench.mjs` defaults to **50 clients / 3 s**, and CI runs `20 clients /
  1500 ms`. There is no sustained, high-concurrency, or soak test, and no
  multi-room test. The README latency/throughput table is a single-box
  indication, not a reproduced CI result.

### M-2 — Sequence CRDT is a foundation, not a feature
- `internal/sequence` exists (62% cover) but is not wired into any demo or
  product path; no end-to-end text-collaboration test.

### M-3 — CI is tests-only
- No coverage gate, no `govulncheck` / SAST, no dependency update bot, no image
  build/publish, no release automation, no `golangci-lint`.

### M-4 — Browser E2E can silently pass without running
- `.github/workflows/ci.yml:36` runs the browser test only
  `if command -v firefox && command -v geckodriver`, otherwise prints
  "Skipping". A green run therefore does not prove the browser path ran.

### M-5 — Cluster paths are only unit-tested in-process
- `bus_test.go` uses `MemoryClusterBus` + explicit `NodeID`. Nothing exercises
  `HTTPClusterRelay` → `ServeClusterEvent` end-to-end (which, per B-1, would
  fail today).

### M-6 — Admin surfaces have no rate limiting / lockout
- `/stats`, `/metrics`, and `/cluster/event` rely solely on the bearer token
  (body capped at 1 MB). No brute-force or request-rate protection.

### M-7 — Browser E2E is flaky (geckodriver startup race)
- `test/browser_kanban_e2e.mjs` waited a fixed 800 ms for geckodriver and then
  POSTed `/session`; a slower cold start fails the whole job with `fetch failed`.
  This turned CI red on a **docs-only** commit (run `39302724795`).
- Patched in the follow-up commit to poll `/status` (up to 15 s) instead of a
  fixed sleep, but the lesson stands: the browser job's reliability depends on
  the runner image and should not be the thing that decides "green".

---

## Low / accuracy

### L-1 — "dependency-free binary" is imprecise
- `go.mod` requires `github.com/gorilla/websocket v1.5.3`. The binary is
  self-contained at *runtime* (static, no external services), but it does have a
  compile-time dependency. Reword the README/DESIGN claim ("no runtime
  dependencies" is accurate; "dependency-free" is not).

### L-2 — Benchmarks are not CI-reproduced
- README states "Measured (50 concurrent clients …)"; CI runs 20 clients / 1.5 s.
  Label the table as indicative of a single-box run, or reproduce it in CI.

### L-3 — "survives kill -9" should not imply power-loss durability
- True for process kills (proven by `make persist`); do not let it read as a
  crash-safe/fault-tolerant guarantee (see B-3).

---

## README claims vs reality

| README claim | Reality |
| --- | --- |
| "single dependency-free binary" | Runtime self-contained; 1 Go build dep (gorilla) — L-1 |
| "persists to disk, survives `kill -9`" | True for `SIGKILL`; no fsync → not power-loss safe — B-3 |
| "Measured … 50 concurrent clients" | Not run in CI (CI uses 20/1.5 s) — M-1, L-2 |
| "Proven correct … all green in `make all`" | True, *when firefox/geckodriver are present* — M-4 |
| Roadmap "Still ahead: hosted demo, npm SDK" | Accurate; add: releases, multi-node, durable fsync, backups — H-1…H-4 |

---

## Definition of done, per target

**Self-host beta (one node, trusted network)**
- [ ] H-1: tag a release, publish the npm SDK, publish a container image
- [ ] H-3: safe auth defaults + loud warnings on public bind
- [ ] H-4: automated backup script + a restore test in CI
- [ ] M-3 / M-4: coverage gate, vuln scan, and a browser-E2E step that fails
      (not skips) when the browser is unavailable
- [ ] L-1 / L-2: correct the durability and benchmark wording

**Multi-node scale**
- [ ] B-1: wire a real `ClusterBus`, make `/cluster/event` accept relayed events
- [ ] B-2: `-node-id` + `-node-peers`, unique-identity enforcement
- [ ] B-3: configurable fsync durability contract
- [ ] M-5: two-node integration test through the HTTP relay

**Public launch**
- [ ] Everything above, plus a hosted demo URL, live status, and published docs

---

## Audit limitations

- Point-in-time at `d042169`; later commits may change specifics.
- No penetration test, fuzzing, load/soak run, or real power-loss simulation was
  performed. B-3's conclusion is from reading `store.go`, not from a fault
  injection run.
- Multi-node conclusions (B-1/B-2) are from static tracing plus endpoint probes;
  no two-node deployment was attempted because the binary cannot configure one.
