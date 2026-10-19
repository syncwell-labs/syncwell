# Syncwell implementation phases

This is the execution plan for resolving the production audit. The monorepo is
the canonical implementation; the split repositories are retained only as
references until this work has a green, reproducible release pipeline.

## Phase 1 — Data integrity (in progress)

**Goal:** ensure replicas and persistent rooms cannot silently diverge or cross
data boundaries.

- [x] Deterministic tie-break for conflicting equal-timestamp registers in Go
  and the JavaScript SDK.
- [x] Regression tests for equal-timestamp value/delete conflicts.
- [x] Collision-free base64 room file paths and restrictive permissions for new
  persistence files.
- [x] Non-destructive migration from legacy sanitised paths, plus collision and
  migration tests.
- [x] JavaScript SDK regression tests.
- [ ] Run Go unit/race tests and cross-language protocol conformance tests.

**Compatibility:** existing legacy log files are copied into their new unique
path when a room is first loaded. The legacy file remains because an old
collision cannot be attributed safely; operators should back up data and review
any legacy room IDs containing characters previously replaced with `_`.

**Verification status:** `node --test` passes in `sdk/`, and `git diff --check`
is clean. Go verification is pending because the local environment currently has
no `go` or `gofmt` binary available.

## Phase 2 — Server safety and durability

**Goal:** reject abusive/invalid traffic and never acknowledge a write as durable
when persistence fails.

- [x] Validate WebSocket frames, register/key/room/node/value sizes, and JSON
  shape.
- [x] Add default room, key, state, value, presence, frame, and max-client
  limits.
- [x] Reject undurable winning writes when persistence append fails.
- [x] Repair slow-client eviction bookkeeping and add regression tests.
- [x] Add operation rate limits and configurable resource-limit knobs.
- [x] Define readiness semantics and a coordinated hub,
  room, client, and store shutdown lifecycle.
- [ ] Run Go unit/race tests and lifecycle/failure-path integration tests.

**Verification status:** regression tests were added for validation,
persistence-failure rejection, slow-client eviction, configured limits, message
rate limiting, and coordinated shutdown. They are pending execution until a
local Go toolchain is available.

## Phase 3 — Build, container, and release pipeline

**Goal:** produce a reproducible image that starts and proves its basic service.

- [x] Restore a minimal Docker build without private-repo credential plumbing.
- [x] Ensure the image serves `-static /demo` and `-sdk /sdk` as non-root.
- [x] Add CI container build/start smoke coverage for `/healthz`,
  `/sdk/syncwell.js`, and WebSocket snapshot delivery.
- [x] Keep CI coverage for formatting-adjacent checks, vet, unit/race, JS, E2E,
  persistence, GC, chaos, benchmark smoke, and build via `make`.
- [ ] Run container smoke locally or in CI with Docker available.
- [x] Add release/version provenance via `-version`, startup logs, ldflags, and
  OCI image labels.

**Verification status:** `node --check test/container_smoke.mjs`,
`node --test` in `sdk/`, and `git diff --check` pass locally. Go and Docker
execution are pending because the local environment currently has no `go`,
`gofmt`, or `docker` binary available.

## Phase 4 — Access control and operations

**Goal:** make a hosted or multi-user deployment safely operable.

- [x] Add SDK room-token transport and safer URL query construction.
- [x] Surface malformed frames and server `error` frames through SDK `error`
  events.
- [x] Protect `/stats` with an optional admin token while keeping `/healthz`
  usable for liveness checks.
- [x] Add TLS/reverse-proxy, token, backup/restore, and monitoring guidance in
  `docs/OPERATIONS.md`.
- [ ] Design principal-aware room capabilities (read/write/presence/admin),
  token rotation/revocation, and short-lived token issuance patterns.
- [x] Add structured logs, metrics, alerts, and restore-drill automation.
- [x] Establish explicit GC/offline lease or epoch semantics to prevent stale
  resurrection after tombstone collection.

### Phase 4 subplan

1. Finalize capability claims and token revocation rules. ✅
2. Wire principal-aware authorization into room admission and privileged
   endpoints. ✅
3. Define room epoch / lease behavior for GC and stale reconnects. ✅
4. Add structured logs, metrics, alerts, and restore-drill automation. ✅
5. Execute Go unit/race and failure-path integration tests once the Go toolchain
   is available.

**Verification status:** SDK tests cover token URL construction, malformed
inbound JSON, and server error frames. Auth tests now cover capability token
round-trips, legacy compatibility, principal/subject claims, `admin`
capability parsing, and revoked JTI rejection on websocket admission. Epoch
regression tests now reject stale `op`, `merge`, and `presence` traffic after
GC-driven room bumps. Structured logs now emit JSON events for startup,
shutdown, GC, compaction, and persistence failures; `/metrics` is
admin-protected, and privileged endpoints now accept a dedicated `admin`
capability token; restore-drill automation lives in `test/restore_drill.mjs`.
Go admin-token tests are pending execution until a local Go toolchain is
available.

## Phase 5 — Product quality and scale

**Goal:** make the SDK and reference product reliable and accessible.

- [x] Add opt-in durable offline SDK storage with restored CRDT state and
  Lamport clock.
- [x] Add typed `SyncwellError` events for malformed frames, server errors, and
  storage failures.
- [x] Harden SDK connection lifecycle against duplicate `connect()` calls and
  overlapping reconnect timers.
- [x] Enable stable client id plus local persistence in the text demo.
- [x] Add protocol version negotiation.
- [x] Improve keyboard interaction, focus states, and mobile layouts in the
  demo surfaces.
- [x] Add browser E2E and visual/accessibility testing.
- [ ] Reassess horizontal scaling and a sequence CRDT only after the preceding
  safety and operations milestones are complete.

**Verification status:** SDK tests cover persistence restore, URL/token
construction, typed error events, protocol version checks, and idempotent
`connect()` behavior. Browser E2E and visual/accessibility checks pass via
`node --test test/browser_kanban_e2e.mjs` when Firefox + geckodriver are
available. The kanban demo also has an accessibility smoke test in
`test/demo_a11y.test.mjs`. A sequence-CRDT foundation now exists in
`internal/sequence` and `sdk/sequence.js` as the next text-collaboration
stretch track.
