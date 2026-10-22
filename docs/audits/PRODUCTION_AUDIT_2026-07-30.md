# Syncwell Production Software Audit and SRS

**Audit date:** 2026-07-30  
**Scope:** all tracked application, test, build, deployment, SDK, demo, and documentation files; the current working-tree `Dockerfile` change was reviewed separately and was not altered.  
**Method:** static code and configuration review plus execution of the JavaScript CRDT suite. The Go toolchain is unavailable in this audit environment, so Go/race/E2E/Docker results are not independently verified.

## 1. Executive summary

Syncwell is an MIT-licensed, self-hostable real-time collaboration engine. A Go HTTP/WebSocket process hosts rooms; a zero-dependency JavaScript SDK keeps a client-side LWW-map CRDT replica; plain HTML demos show shared notes and a kanban board. Its strongest qualities are small scope, a clear ownership model (one goroutine owns each room), understandable CRDT documentation, and meaningful convergence, persistence, and GC test intent.

It is **not production-ready for untrusted or enterprise workloads**. The central correctness guarantee can be broken by equal forged timestamps, room IDs collide on disk after sanitisation, authorization is binary room-wide read/write access, input and resource limits are inadequate, persistence failures do not fail writes, and graceful shutdown does not own the WebSocket/room lifecycle. Docker runtime serving is also broken as currently configured because `/sdk/` is copied but not selected with `-sdk /sdk`.

**Release recommendation:** suitable for a local demo or trusted prototype after confirming the committed build; do not market as production-ready or accept untrusted clients until the Critical/High roadmap items are completed and tested.

## 2. Project summary

| Area | Observed implementation |
| --- | --- |
| Purpose | Embed low-latency shared JSON state, presence, and offline convergence into web applications without a hosted collaboration vendor. |
| Users | Product/platform developers self-hosting Syncwell; their end users collaborate through an integrating application. |
| Roles | No application users/roles exist. A bearer token (optional) permits a connection to one room with full read/write/presence ability. Server operator configures flags. |
| Architecture | One Go process; HTTP health/stats/static handlers; Gorilla WebSocket endpoint; lazily-created room goroutine; client CRDT SDK; optional file op-log. |
| Technologies | Go 1.23, `github.com/gorilla/websocket`, standard library; browser/Node ESM; plain HTML/CSS; Docker; GitHub Actions; Make. |
| Dependencies | One Go runtime dependency, zero SDK runtime dependencies. Node is development/test-only. |
| Storage | Optional per-room newline JSON op-log, periodically compacted to current registers. No database, object store, queue, or cache. |
| Integrations | WebSocket, Docker registry/base images, GitHub Actions, optional npm publication. No email, analytics, notification, or identity provider. |

### Data and user flow

1. An integrator creates `Syncwell(baseURL, room)`, subscribes to `change`, then connects.
2. The server optionally checks Origin and a room-scoped HMAC token, upgrades `/ws`, joins a room, and sends a register snapshot.
3. SDK writes update its local LWW map immediately, then send a client-stamped register when connected. Offline writes remain in memory only.
4. The room merges the register, appends a winning write where persistence is enabled, and fans out the current winner. Reconnecting clients send a full state `merge` and receive a new snapshot.
5. Presence is relayed only; acknowledgements advance a connected-client low-water mark used for periodic tombstone collection.

### API flow

`GET /healthz` returns `ok`; `GET /stats` returns room/client/CRDT counters; `GET /ws?room=<id>&token=<token>` upgrades to JSON WebSocket frames. The protocol is documented in `docs/protocol/PROTOCOL.md`; it has no HTTP CRUD API, pagination, filtering, sorting, or version negotiation.

## 3. Module and feature inventory

| Feature/module | Inputs → outputs and logic | Storage/API/permission | Key edge cases or limits |
| --- | --- | --- | --- |
| LWW map and Lamport clock | key, JSON value/delete, timestamp → winning register/live state. Higher `(counter,node)` wins. | In-memory and protocol registers; no access check. | Equal timestamp but different payload has no deterministic tie-break; see C1. |
| Offline SDK sync | local writes, socket events → optimistic `change`, `op`, `merge`, snapshot convergence. | WebSocket; client has full room state in memory. | Browser reload loses offline edits; no outbound queue or durable client store. |
| Room hub | join/leave/op/merge/presence/ack commands → snapshot/fan-out/eviction. | `/ws`; all authenticated users can mutate all keys. | Commands/state payloads are not schema-validated or rate-limited. |
| Presence | arbitrary JSON → broadcast to peers. | WebSocket only; no storage. | Not replayed; arbitrary payload is accepted under the 1 MiB frame cap. |
| Persistence | room and winner registers → buffered append log; compaction. | `-data`; local filesystem. | Sanitised IDs alias; append failures are logged but writes continue; no power-loss guarantee. |
| Tombstone GC | client clock acknowledgements → low-water threshold and deletion. | `/stats` exposes counts. | Offline clients are intentionally excluded and stale writes can resurrect deleted keys. |
| HMAC token auth | secret, room, expiration → signed bearer token. | `-secret`, `-mint`, `token` query parameter. | No identity, roles, revocation, key rotation, scopes, or read-only grants. |
| Origin/client cap | exact Origin strings, room client count → 403 or room-full error. | `-origins`, `-max-clients`. | Missing Origin is accepted even with allow-list; there are no global/per-IP limits. |
| Demo UI | text/card operations → DOM rerender. | Static `/`, `/kanban.html`, `/sdk/`. | Docker entrypoint omits `-sdk /sdk`; demo imports fail in the image. |
| Tests/CI/bench | test commands → test outcomes/benchmark markdown. | Make and GitHub Actions. | No security, HTTP/WebSocket contract, UI/E2E browser, load-abuse, or coverage reporting. |

## 4. Findings and gap analysis

### Critical

**C1 — CRDT convergence is not guaranteed for adversarial/equal timestamps.** `Register.merge` retains the existing value when timestamps tie. Two clients can submit the same `(counter,node)` with different value/deleted state; replicas receiving them in different orders retain different values. The server neither binds timestamp node IDs to a connection nor rejects duplicates with divergent content. This invalidates the advertised commutativity/convergence claim for malformed, buggy, or hostile clients. **Fix:** make a register identity unique and validate it server-side, or add a deterministic payload hash/deletion/value tie-breaker implemented identically in Go and JS. Add equal-timestamp property, protocol-abuse, and cross-language tests.

**C2 — Room IDs can collide in persistent storage.** `FileStore.path` replaces all non `[a-zA-Z0-9_-]` characters with `_`; for example `a/b` and `a_b` map to the same log. A token for one room can therefore access another room's persisted document, creating data loss and authorization bypass. **Fix:** use a collision-resistant encoding such as URL-safe base64 or SHA-256 filename plus stored original room ID; reject oversized IDs; migrate safely. Test collisions and traversal.

### High

**H1 — Docker demo cannot serve the SDK.** The image copies the SDK to `/sdk`, but the entrypoint supplies only `-static /demo`; the program defaults `-sdk clients/javascript`, which does not exist from the distroless working directory. `/sdk/syncwell.js` returns 404 and both demos fail. **Fix:** add `-sdk /sdk`, then add a container smoke test fetching `/`, `/sdk/syncwell.js`, `/healthz`, and a WebSocket connection.

**H2 — Persistence failures acknowledge and broadcast non-durable writes.** An `Append` error is only logged; clients receive an `op` even though a restart may lose it. Compaction errors are also only logged. **Fix:** select a documented consistency mode: fail the write/close the room, mark the room unhealthy/read-only and alert, or explicitly label best-effort. Return structured errors and health status; test disk-full/permission/I/O failure.

**H3 — No payload schema, key/room limit, per-client/global rate limit, or room lifecycle limit.** A client can force unbounded rooms, keys, state maps, JSON depth/size below 1 MiB, persistence volume, broadcasts, and CPU. `merge` processes every map member serially in the room goroutine. **Fix:** validate frame type and schema; cap room/key/node/value/state sizes, keys/room, rooms/server, operation rate, presence rate, connections globally/per IP, and merge work; expose rejection metrics.

**H4 — Authorization has no identity or least privilege.** Valid token holders receive snapshot data and can write/delete every key, send presence, and retain access until expiry. Tokens in query strings can leak through proxies/logs. **Fix:** define authenticated principals and room ACLs/scopes (`read`, `write`, `presence`, admin), use short-lived signed claims or an identity-provider integration, support revocation/key rotation, and prefer a secure WebSocket subprotocol/cookie/header exchange where appropriate.

**H5 — Shutdown and eviction leave room state inconsistently managed.** WebSocket pumps are detached goroutines; `http.Server.Shutdown` does not explicitly coordinate their end. Store close can race logically with room appends. Slow-client eviction removes `clients` but does not remove `acked`; its later `leave` sees no client and also does not remove the stale acknowledgement, pinning low-water GC and leaking client references. **Fix:** introduce hub/room context cancellation and `Hub.Close`, unregister/close every client deterministically, remove both maps in one helper, wait for pumps/rooms before `Store.Close`, and add race/lifecycle tests.

**H6 — Tombstone GC has a documented but serious data-integrity boundary.** Because disconnected replicas do not hold back low-water, an old offline write may resurrect a key whose delete tombstone was collected. **Fix:** retain per-replica version knowledge/server epochs, require bounded offline leases plus client reset, or disable GC when durable offline edit is promised. Make the policy enforceable and test stale reconnect after GC.

### Medium

**M1 — Input validation and protocol error handling are silent.** Malformed frames are ignored; invalid/missing key/register, invalid timestamp/node, unknown fields, and room-full are not a stable typed contract. This impairs client recovery and observability. Add versioned schema validation, close codes/error frames, and an OpenAPI-like WebSocket message specification.

**M2 — Security transport/deployment controls are absent.** TLS, proxy header policy, secure cookie/token guidance, security headers, operator authentication for `/stats`, and secret sourcing/rotation are not supplied. `/stats` leaks room IDs and activity publicly. Document a reverse-proxy deployment or provide TLS configuration; protect/disable stats in production.

**M3 — File-store recovery hides all malformed log lines.** `Load` skips any invalid line, not just a torn final record, which can silently discard corruption. Compaction error paths leave temp files. Use checksums/sequence IDs, accept only an invalid final record, alert on corruption, and clean temp files safely.

**M4 — Browser SDK reliability gaps.** It parses inbound JSON without `try/catch`, so a bad server frame can throw; reconnect timers can overlap under repeated `connect`; base URLs already containing query parameters create an invalid URL; there is no token option, outbound durability, jitter, visibility handling, or telemetry. Fix and test each API behavior.

**M5 — UI accessibility and responsiveness are demo-level.** Kanban uses clickable non-button avatars, icon-only delete buttons without accessible names, no visible focus/keyboard drag/drop path, limited error/empty/loading states, and three fixed columns that do not adapt well to narrow screens. Add semantic controls, focus styles, ARIA/live status, keyboard card movement, responsive grid, and automated accessibility checks.

**M6 — Observability is insufficient.** Plain process logs lack structured fields, log levels, correlation/room/client safety policy, metrics, traces, error counts, connection counts, latency histograms, persistence/GC health, alerts, or audit trail. Add structured logging and metrics endpoint/OTel, then dashboards and alert thresholds.

**M7 — Benchmark evidence is useful but narrow.** It is single-host loopback, mutable output is ignored by git, and it measures neither WAN nor sustained multi-room/load-abuse behavior. Keep it as a smoke benchmark, record machine/Go/Node versions and distribution, and add reproducible capacity tests with thresholds.

### Low / documentation and maintainability

**L1 — Documentation overstates readiness.** README/PLAN/project summary say production-ready/100% despite the limitations above; `README` says GitHub CI badge should be replaced, and launch placeholders remain. Align claims with supported threat model and operational guarantees.

**L2 — Configuration is flag-only and lightly validated.** No config file, example environment file, startup validation, defaults matrix, or deployment reference. Add a configuration reference and reject unsafe combinations in production mode.

**L3 — Test coverage is concentrated in CRDT behavior.** Existing tests cover core laws, selected storage cases, token round-trip, offline/persistence/GC scenarios, chaos simulation, and benchmark. Missing: server HTTP/WS auth/origin/cap/frame handling; failure paths; Docker; accessibility; API compatibility; fuzzing; coverage gates; restore/backup; security tests.

## 5. Code quality, database, and security assessment

### Architecture and code review

The separation into `crdt`, `server`, `store`, SDK, demos, and tests is clean and understandable. The room-owned goroutine is a good KISS choice for a single process. Go/JS merge logic is intentionally mirrored. Main maintainability risks are duplicated protocol/CRDT representations without generated shared conformance fixtures, implicit failure semantics, global mutable lifecycle, and misleading “correct”/“durable” language that hides unsupported cases.

There is no relational database. The file log is a denormalised materialised state/op stream. It has no schema version, checksums, encryption, indexes, backup/restore tooling, retention, tenant isolation, quota, migration, or collision-free primary key. File permissions are `0755` directory and `0644` logs, which may expose collaboration content to other local users depending on host policy. Use an operator-selectable restrictive mode (for example `0700`/`0600`) and document ownership.

OWASP-relevant posture: no SQL injection surface; JSON values can become DOM XSS only if an integrating UI uses unsafe HTML (the shipped demos mostly use `textContent`); CSRF is less applicable to bearer WebSockets but Origin/token controls must be strict; availability/resource exhaustion is the largest direct risk; data exposure occurs via room/storage collision, public stats, logs/query tokens, and permissive files; no audit/nonrepudiation trail exists.

## 6. Production readiness scorecard

Scores reflect the code reviewed, not the project’s self-reported status.

| Category | Score /10 | Rationale |
| --- | ---: | --- |
| Architecture | 6 | Clear small-process design; lifecycle and malicious-input correctness gaps. |
| Backend | 5 | Functional core, but no validation/rate limits/failure contract. |
| Frontend/SDK | 5 | Simple local-first SDK; no durable offline state or robust protocol handling. |
| Database/persistence | 3 | Useful append/compact design, but room collision and durability/error gaps. |
| Security | 3 | Basic HMAC/origin hooks only; no ACLs, limits, TLS/deployment posture, or audit. |
| Testing | 5 | Strong CRDT-focused tests; broad system/security/failure/UI gaps. |
| Performance | 5 | Good loopback baseline; no quotas or demonstrated production profile. |
| Scalability | 3 | Single process/room owner; no multi-node, tenant, or lifecycle controls. |
| Maintainability | 6 | Small/readable/documented; claims and protocol duplication increase risk. |
| Documentation | 6 | Good design/protocol/contribution docs; missing operational/SLA/security documentation. |
| Deployment/operations | 3 | CI/Docker exist, but current Docker demo defect, no monitoring/backups/rollback. |

## 7. Software requirements specification (target state)

### Business goals and roles

Provide a self-hostable collaboration service that lets application developers add shared state while operators retain deployment and data control. Roles: **operator** (configures/deploys/observes/backups), **integrator** (obtains credentials and selects rooms), and authenticated **end user** with explicit room permissions. The server must not infer user identity from a bearer token alone.

### Functional requirements and acceptance criteria

1. **Room collaboration:** authorised writers can set/delete validated JSON values; authorised readers receive a convergent snapshot and updates. Given any valid operation set, all replicas converge deterministically regardless of duplicate/reordered delivery.
2. **Offline sync:** SDK persists unsent/local state in an explicitly configured browser store; after reconnect it converges or returns a clear reset/conflict error if its lease/epoch expired.
3. **Presence:** authorised clients can publish bounded, rate-limited ephemeral state, which is never persisted and is removed on disconnect.
4. **Access control:** operator/integrator can grant least-privilege room capabilities, expiration, revocation, and key rotation; unauthorised actions receive documented close/error codes.
5. **Persistence:** acknowledged durable writes survive a documented failure model; a failed persistence write is never reported as committed; backup and restore are tested.
6. **Operations:** health/readiness/metrics/logs provide enough evidence to operate and alert on connection, latency, rejection, storage, GC, and error conditions.

### Non-functional requirements

- Enforce documented limits per frame, key, value, room, client, tenant, IP, and server; reject excess with metrics.
- Use TLS in production and avoid credential leakage in URLs/logs.
- Define supported browsers/Node/Go versions, protocol versioning, backward compatibility, SLOs, RPO/RTO, backup retention, and incident/rollback process.
- Meet WCAG 2.2 AA for shipped demos/reference UI and responsive behavior at 320 px+.
- Use structured logs without values/tokens by default and an immutable operator audit trail for sensitive administrative actions.

### Target data model

`Room(id, tenant, epoch, createdAt, limits)`; `Membership(roomId, subjectId, capability, expiry, revokedAt)`; `Register(roomId, key, timestampCounter, timestampNode, value, deleted, deterministicHash)`; `ReplicaLease(roomId, replicaId, acknowledgedVersion, expiry)`; append segments/snapshots with checksum/schema version; `AuditEvent(actor, action, roomId, time, outcome)`. This can begin in embedded storage but must preserve collision-free IDs, constraints, integrity checks, restrictive permissions, and backup compatibility.

### Target API/UI/security/testing/deployment specification

Version WebSocket frames (`v`, `type`, request/correlation id) and publish schemas, limits, close codes, permission behavior, error format, and compatibility policy. The SDK exposes `token`/credential configuration, typed error events, connection state, retry policy, and persistent offline adapter. Reference UIs must have live connection/error state, keyboard interaction, accessible labels/focus, empty states, and narrow-screen layout.

CI must run Go vet, unit/race/fuzz tests, JS unit and browser E2E tests, container smoke/security scans, protocol conformance across Go/JS, and controlled failure tests. Deployment must run as non-root with `-sdk /sdk`, TLS/proxy guidance, read-only image where feasible, secrets from a secret manager, persistent volume ownership, readiness/liveness, metrics/log aggregation, encrypted backups, restoration drills, and a versioned rollback plan.

## 8. Implementation roadmap

| Priority | Work | Complexity / dependencies | Risk if deferred | Estimate |
| --- | --- | --- | --- | --- |
| Critical | Correct equal-timestamp merge; validate/bind operation identity; Go/JS conformance tests | Medium; protocol compatibility decision | Split-brain state/data integrity | 3–5 days |
| Critical | Replace colliding room file naming and safely migrate storage | Medium; storage version/backup | Cross-room data exposure/loss | 2–4 days |
| High | Add strict frame/schema/size/rate/resource limits | High; metrics/config | DoS/cost/room starvation | 1–2 weeks |
| High | Make persistence acknowledgement and shutdown semantics correct; fix stale `acked` eviction | High; lifecycle tests | Data loss, leaks, unavailable GC | 1 week |
| High | Fix Docker `-sdk /sdk`; add image smoke test | Low | Shipped demo unusable | <1 day |
| High | Design ACL/identity/token lifecycle and remove query-token exposure | High; product/security decisions | Unauthorised mutation/data disclosure | 2–3 weeks |
| Medium | Define GC/offline lease or epoch model | High; client storage/protocol | Stale resurrection or unbounded storage | 1–2 weeks |
| Medium | Metrics, structured logs, protected stats, alerts, backups/restore | High; operational platform choice | Undetectable failures/no recovery | 2–3 weeks |
| Medium | SDK hardening and durable offline adapter | Medium | Fragile reconnect/offline experience | 1 week |
| Medium | Browser E2E/accessibility/responsive reference UI | Medium | Poor usability and integration confidence | 1 week |
| Low | Protocol versioning, config reference, claims correction, runbook | Medium | Support/upgrade friction | 3–5 days |
| Low | Horizontal sharding/sequence CRDT | Very high; only after correctness/operations | Capacity/product scope limitation | Separate roadmap |

## 9. Verification record and audit limits

- **Passed in this environment:** `cd clients/javascript && node --test` — 1 JS CRDT test file passed.
- **Not runnable here:** `go` is not installed; therefore `make vet`, Go unit/race/chaos, build, Node E2E (requires the build), and Docker build/run were not executed.
- **Reviewed but not modified:** current uncommitted Dockerfile and untracked `projects_summary.md`; no source, configuration, or existing user change was altered by this audit.

## 10. Residual risks and future improvements

After the priority work, reassess multi-tenancy, end-to-end encryption, compliance/data retention, region failover, sharding, sequence CRDT support, SDKs in other languages, hosted control plane, and product analytics. Do not add these before deterministic convergence, storage isolation, durable commit semantics, limits, observability, and a clear security model are demonstrably in place.

## 11. Addendum: split-repository audit

**Additional scope reviewed:** `/home/useer/E/syncwell-engine` and `/home/useer/E/syncwell-public`, including every tracked source, build, CI, SDK/demo, deployment, and documentation file. Both worktrees were clean at review time. No source file in either repository was changed.

### Repository relationship

`syncwell-engine` is a private Go module (`github.com/Nil-Vaghani/syncwell-engine`) containing the CRDT, hub, store, token auth, test suite, and a small `pkg/engine` façade. `syncwell-public` is intended to be public and imports engine `v0.1.1`; it contains only the binary wiring, SDK, demos, public documents, Docker build, and deployment guide.

The engine core is substantively the same as the monorepo reviewed above, apart from import paths. Accordingly, C1–M7 remain applicable to the engine and the deployed public binary. The split introduces release, supply-chain, reproducibility, and delivery risks below.

### Additional critical findings

**SC1 — The public Dockerfile is syntactically unresolved and cannot build.** It contains literal Git merge-conflict markers (`<<<<<<< HEAD`, `=======`, `>>>>>>> ...`) in the instruction stream. Docker treats this as an unknown instruction before it reaches the build. **Impact:** all Docker/Render deployment and Docker CI are blocked. **Fix:** resolve the conflict, review the intended credential mechanism, and make a clean build the first CI gate.

**SC2 — Even after resolving the conflict, the public image cannot start.** The final base is `gcr.io/distroless/base-debian12:nonroot`, which does not contain `/busybox/sh`, but the entrypoint executes `/busybox/sh -c ...`. It also does not pass `-sdk /sdk`, so the demos cannot import their SDK. **Impact:** deployment crashes at startup; a hypothetical start would serve broken demos. **Fix:** avoid shell expansion by making the Go binary honour `PORT`, then use a direct exec-form entrypoint such as `[/syncwell, -static, /demo, -sdk, /sdk]`; add image start/health/static/WebSocket smoke tests.

**SC3 — The public Docker and CI credential contracts disagree, and the Dockerfile exposes an SSH private key to the build layer/cache.** Docker expects `SSH_PRIVATE_KEY` and writes it to `/root/.ssh/id_rsa`; CI supplies `GITHUB_TOKEN` instead. The declared private-key build argument is not provided, and long-lived private material becomes part of a builder layer/cache. **Impact:** private engine download fails and the intended security boundary leaks a credential. **Fix:** use BuildKit `--mount=type=ssh` or `--mount=type=secret`, do not persist secrets in layers, standardise one explicitly named `ENGINE_READ_TOKEN`/SSH mechanism, and rotate any key used with the current design.

### Additional high findings

**SH1 — The public CI cannot reliably read the private dependency.** It refers to `secrets.GITHUB_TOKEN`; GitHub provides this as an ephemeral token scoped to the current repository, not a fine-grained PAT with access to another private repository. The documented instruction to configure a PAT under that reserved name is unsafe/incorrect. **Fix:** create a separately named repository/organisation secret (for example `ENGINE_READ_TOKEN`) with least privilege, use it only in a BuildKit secret/credential helper, and test the workflow from a clean runner.

**SH2 — “Hiding” the engine is not a security control and has fragmented the release/test system.** Go links the engine code into the public binary; a private source repository does not protect an algorithm in a distributed executable. The public CI only vet/builds the wrapper and runs one SDK test; it does not run the engine’s offline, persistence, GC, or chaos scenarios. The binary-inspection test searches for `syncwell/internal`, but actual engine paths are `syncwell-engine/internal`, so it cannot prove its stated assertion. **Fix:** treat the split as a packaging/release choice, not security; use semantic versioning, signed tags/checksums, a public compatibility suite, and a cross-repository release pipeline that tests the exact pinned engine/public pair.

**SH3 — The engine repository’s own documented developer workflow is broken.** It contains no `clients/javascript/`, `examples/demos/`, or `tests/` directory, but its `Makefile` still declares `make all` targets that `cd clients/javascript` and invoke Node E2E files. Its README instructs users to open demo pages that are absent and refers to an absent `docs/architecture/DESIGN.md`. **Impact:** claimed full validation cannot run from the engine repo; contributors receive misleading instructions. **Fix:** move shared tests/demo assets into a dedicated conformance repository/package, or make engine targets Go-only and execute end-to-end tests from the public repo against a pinned engine version; correct the README/docs.

**SH4 — The public module manifest is not reproducible as committed.** `go.mod` includes a direct `gorilla/websocket` requirement unused by the public wrapper and has no `go.sum`; Docker runs `go mod tidy` against the copied source, mutating the module graph during a build. **Fix:** run `go mod tidy` deliberately, commit `go.sum`, remove unused direct dependencies, and make CI fail on a dirty module graph after verification.

**SH5 — Deployment documentation makes stale and unsafe assertions.** `DEPLOY.md` says the Docker entrypoint is correct, promotes a build-argument credential strategy, and instructs an image-history token check that cannot establish secrecy. Hosting pricing, timeout, and free-tier statements are time-sensitive and require periodic verification. **Fix:** correct the build/run instructions only after a smoke-tested image exists; replace provider-specific claims with dated links/verified assumptions; document supported failure/recovery/security constraints from this audit.

### Revised split-release plan

1. **Stop release/deploy:** rotate any private key used as a Docker build arg; resolve Docker conflict; build and run the public image locally with a non-secret fixture/module source.
2. **Restore reproducibility:** commit tidy `go.mod`/`go.sum`; tag the engine; pin and verify the exact engine version; produce SBOM/provenance and dependency checksums.
3. **Build a secure dependency path:** use GitHub App/deploy key or narrowly scoped token via BuildKit secrets; never use an ARG or leave key files in a Docker layer; use distinct secret names and rotation runbooks.
4. **Create a cross-repository release gate:** build the public binary/image, run Go engine race/chaos/storage tests, SDK tests, browser/WebSocket offline/persistence/GC tests, and an image smoke test against the pinned module before publishing/deploying.
5. **Rationalise repository ownership:** either reunify until a legitimate packaging boundary is needed, or publish a stable engine API/module with generated protocol-conformance tests and accurate separate contribution/release documents.

### Updated release disposition

The split public deployment is **blocked** until SC1–SC3 are corrected and independently verified. The engine remains a prototype with the core safety gaps already listed; splitting it into a private module does not raise its production readiness score.
