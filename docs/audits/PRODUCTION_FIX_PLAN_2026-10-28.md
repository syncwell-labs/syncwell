# Production fix plan — 2026-10-28

**Status:** local implementation and the full local verification matrix are
complete (2026-10-29). P0-1 through P0-5, all P1, and the local P2 work pass
every local gate; remaining items are external cutover/CI confirmation and
owner-only release work, tracked below.

**Baseline:** `3c69754 feat(security): harden production defaults and runtime limits`
(clean tree). All existing gates are green: `make all`, `go test -race ./...`,
`make cover-check`, `make docs-build`, `make a11y`,
`make browser-e2e-chromium`, `make browser-e2e-firefox`, `make test-python`,
JS tests + `tsc`.

**Relationship to older plans:** this document closes the gaps found in the
2026-10-28 re-audit. It does not repeat the completed work in
[`COMPLETION_PLAN_2026-10-20.md`](./COMPLETION_PLAN_2026-10-20.md). Open items
from that plan are folded in here:

- Phase 2.3 (shared cluster transport) -> **P2-2**
- Phase 2.5 (room ownership / re-homing) -> **P2-2**
- Phase 7 (hosted demo, publish, launch) -> **P3-4** (owner-only, last)

**Rule:** every task names the finding it closes (`F-*`), the files it touches,
and a test that would fail today. No task may be closed by prose alone.

---

## 1. Findings being fixed

| ID | Finding | Severity | Closed by |
| --- | --- | --- | --- |
| F-01 | Browser SDK puts tokens in the query string by default; Python SDK only supports query | High | P0-2 |
| F-02 | Multi-node is full-mesh only; no shared bus, ownership, or re-homing | High (scale) | P2-2 |
| F-03 | No global connection/file-descriptor budget (`MaxClients` is per room) | High | P0-4, P1-7 |
| F-04 | Cluster relay follows redirects with a default HTTP client | High | P0-3 |
| F-05 | No room delete/purge/retention; logs live forever | High | P1-1 |
| F-06 | `/sdk/` exposes `node_modules`, `dist`, tests, package files, and directory listings | Medium | P0-5 |
| F-07 | TLS floor drift: code enforces 1.3, log says 1.2, docs say "1.2+" | Medium | P0-1 |
| F-08 | Blocking `room.cmds <-` sends can park handler goroutines behind a wedged room | Medium | P1-5 |
| F-09 | CI gaps: only kanban browser test; tag-pinned actions; no scheduled fuzz/soak; stale docs origin | Medium | P0-7 |
| F-10 | Org rename incomplete (old `Nil-Vaghani` links, release config, Render digest) | Medium | P0-6 |
| F-11 | Backups are unencrypted and unsigned | Medium | P1-2 |
| F-12 | No audit trail for privileged actions | Medium | P1-3 |
| F-13 | No request IDs / log correlation | Medium | P1-4 |
| F-14 | No saturation/cluster metrics or alerts (fd, goroutines, conns, relay latency) | Medium | P2-1 |
| F-15 | SHA-256 log filenames make room -> file mapping opaque for operators | Low | P1-1 |
| F-16 | Legacy v1 tokens cannot be revoked | Low | Accepted + documented (P3-2) |
| F-17 | `-static` can serve directory listings; file servers follow symlinks | Low | P0-5 |

---

## 2. Decisions to lock before coding

These are defaults, not suggestions. Changing one later is a new decision, not a
mid-task tweak.

| ID | Decision | Chosen value | Rationale |
| --- | --- | --- | --- |
| D1 | Built-in TLS floor | `-tls-min=1.3` by default; `1.2` available | Keeps today's secure behavior, gives legacy users an escape hatch, and removes the code/log/docs contradiction |
| D2 | Token carrier | Browser: WebSocket subprotocol by default. Python: `Authorization: Bearer` by default. Query only with explicit opt-in | Removes credentials from URLs; server already accepts both carriers |
| D3 | Multi-node | Single-node GA ships first. Redis Streams bus + lease ownership/re-homing is a separate, non-release-blocking program (`P2-2`) | Re-homing is not a patch: it needs shared state and an ADR |
| D4 | Retention | Off by default (`-retention=0`); admin purge always available | Destructive defaults are unacceptable |
| D5 | Backup encryption | Encrypted by default for new backups; plaintext requires `-backup-plaintext`; v1 backups remain readable | Fails safe; explicit opt-out keeps migration possible |
| D6 | Global connection cap | `-max-conns=4096` by default; `0` = unlimited | Bounds fds and memory; documented as a per-process limit |
| D7 | Org identity | `syncwell-labs` everywhere current-facing | Finish the rename; keep dated audit snapshots historical |
| D8 | Versioning | Next release `0.5.0` (pre-1.0 minor may break); add `CHANGELOG.md` and migration notes | Token default and backup behavior are observable changes |

### Release gates

| Gate | Meaning | Contents |
| --- | --- | --- |
| A | Safe, operable single node | P0 + P1 |
| B | Observable and supportable | P2-1, P2-4, P2-5 |
| C | Horizontally scalable | P2-2 + P2-3 (ADR-gated, not required for A/B) |

`0.5.0` ships A + B. Gate C is a separate release; do not delay security fixes
for it, and do not claim multi-node re-homing until Gate C passes.

Suggested execution order:

1. P0-1, P0-2, P0-3, P0-5 in parallel (independent, small).
2. P0-4, then P0-7; P0-6 last among P0 tasks because its Render line waits for
   a new image digest.
3. P1-4, then P1-3; P1-1, P1-2, P1-5, P1-6, P1-7 in parallel.
4. P2-1, P2-4, P2-5; cut `0.5.0`. Only then start P2-2 (ADR) and P2-3.

---

## 3. Phase P0 — Correctness and safe defaults (Gate A)

Target: 1-2 working weeks. Tasks are independent except where noted.

| Task | Closes | Size | Depends | Acceptance (must fail before, pass after) |
| --- | --- | --- | --- | --- |
| P0-1 TLS truth | F-07 | S | — | Startup log prints the configured minimum; docs say 1.3 by default and 1.2 as opt-in; a TLS 1.2 client is rejected by default and accepted with `-tls-min=1.2` |
| P0-2 Token carrier defaults | F-01 | M | — | Default JS/Python connections contain no `token=` query parameter; subprotocol and `Authorization` E2E pass; query works only when explicitly enabled |
| P0-3 Cluster redirect guard | F-04 | S | — | A peer returning `307`/`308` is treated as an error and the redirect target is never contacted |
| P0-4 Global connection budget | F-03 | M | — | With `-max-conns=1`, the second concurrent WebSocket is rejected with HTTP 503 and `Retry-After`; gauge returns to zero after disconnect |
| P0-5 Safe static/SDK serving | F-06, F-17 | M | — | `/sdk/package.json`, `/sdk/node_modules/`, `/sdk/`, and symlinks return 404; allow-listed bundles return 200 |
| P0-6 Finish org rename | F-10 | M | P3-1 for Render only | `rg -i 'nil-vaghani'` is clean outside `docs/audits/`; docs build has no broken internal links; Render still deploys |
| P0-7 CI truthfulness | F-09 | M | P0-1..P0-5 | Chromium and Firefox suites run on every PR; actions are SHA-pinned; fuzz runs on a schedule; docs canonical origin is the new org |

### P0-1 — TLS floor (F-07)

Files: `cmd/syncwell/main.go`, `cmd/syncwell/main_test.go` (new helper test),
`docs/operations/DEPLOY.md`, `website/self-hosting/quickstart.md`,
`website/self-hosting/configuration.md`, `docs/operations/THREAT_MODEL.md`.

1. Add `-tls-min` / `SYNCWELL_TLS_MIN` with values `1.2` and `1.3`
   (case-insensitive). Reject anything else at startup.
2. Build `tls.Config{MinVersion: ...}` from the parsed value. Do not set
   `MaxVersion`; let Go negotiate 1.3 when both sides support it.
3. Replace the hard-coded `"min_version": "1.2"` log field with the parsed
   value.
4. Add a table-driven test for the parser and one `tls.Dial` test:
   default server rejects a client capped at 1.2; `-tls-min=1.2` accepts it.
5. Update docs tables and the threat-model row. Remove "TLS 1.2+" ambiguity:
   state "TLS 1.3 by default, TLS 1.2 opt-in".

Do **not** add cipher-suite lists; Go's defaults are safer than a hand-picked
list, and TLS 1.3 suites are not configurable in `crypto/tls`.

### P0-2 — Token carrier defaults (F-01)

Files: `clients/javascript/syncwell.js`,
`clients/javascript/syncwell.d.ts`, JS tests, `clients/python/syncwell_client.py`,
`clients/python/test_client_e2e.py`, `tests/e2e/browser_chromium_e2e.mjs`,
`tests/e2e/browser_firefox_e2e.mjs`, `tests/e2e/container_smoke.mjs`,
`website/guide/*`, `website/reference/protocol.md`,
`examples/demos/*` (only where the SDK is constructed).

1. JS: change the default to `"subprotocol"`. Keep `"query"` as an explicit
   opt-in; never silently fall back. Send the token as
   `syncwell.token.<token>` exactly as the server's `websocketToken` expects.
2. Python: send `Authorization: Bearer <token>` in the handshake (the server
   already accepts it). Keep `allow_query_token=False` as the default; expose
   `allow_query_token=True` only for old servers.
3. Server (defense in depth): when a query token is used, emit a rate-limited
   `LogEvent("ws_token_query_deprecated", ...)` and increment
   `syncwell_token_in_query_total`. Never log the token.
4. Tests must assert the built WebSocket URL has no `token` parameter in the
   default configuration, and that both carriers authenticate against the real
   server.
5. Docs: remove SDK examples that put tokens in URLs. The demo-page URL
   (`/ ?room=...&token=...`) is a separate browser-app convention; leave it,
   but label it as demo-only.
6. `CHANGELOG.md`: call this out as a breaking SDK default.

### P0-3 — Cluster relay redirect guard (F-04)

Files: `internal/server/bus.go`, `internal/server/bus_test.go`.

1. Give `HTTPClusterRelay` a dedicated client:
   `CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }`.
   Cluster peers have no legitimate redirect flow. The existing `>= 300` branch
   then records `cluster_relay_status_error`.
2. Set a `Transport` with `Proxy: nil`, `DialContext` timeout, and
   `TLSClientConfig.MinVersion = tls.VersionTLS12` for outbound peers; the
   server-side floor (D1) does not constrain outbound compatibility.
3. Validate peer URLs at startup: absolute `https` (or `http` only with
   `-cluster-insecure`), no userinfo, no path/query/fragment. Today
   `peer+r.Path` silently misbehaves if a path is present.
4. Test with two `httptest` servers: peer A returns `307` to peer B; assert B
   received zero requests and `errs` incremented.

### P0-4 — Global connection budget (F-03)

Files: `internal/server/hub.go` (`Config`, metrics), `internal/server/server.go`
(`ServeWS`), `internal/server/ratelimit.go` (IP helper reuse),
`cmd/syncwell/main.go`, `cmd/loadtest/main.go`, docs.

1. Add `Config.MaxConns int64` and `-max-conns` / `SYNCWELL_MAX_CONNS`
   (default 4096, `0` = unlimited).
2. Admit before `Upgrade`: after auth/origin checks, `Add(1)` to an
   `atomic.Int64`; if the result exceeds the limit, `Add(-1)` and return HTTP
   503 with `Retry-After: 5` and a short text body. Do not upgrade.
3. Release exactly once on handler exit (`sync.Once` or only in `readPump`'s
   defer). A rejected upgrade must not leak a slot.
4. Metrics: `syncwell_connections_current`,
   `syncwell_connections_limit` (gauge), `syncwell_connection_rejects_total`
   (counter).
5. `cmd/loadtest` must pass `-max-conns` (or set it high) so the documented
   10k-client run remains valid. Add this to the load-test docs.
6. Keep `MaxClients` per room; document both limits and their product.

Optional follow-up (not Gate A): `-max-conns-per-ip`, keyed by `clientIP`,
with a cleanup map. Do not add it until the global budget is proven.

### P0-5 — Safe static and SDK serving (F-06, F-17)

Files: `cmd/syncwell/main.go`, new `internal/server/static.go`,
`internal/server/static_test.go`, `Dockerfile` (no change expected),
`docs/operations/DEPLOY.md`.

1. `/sdk/`: serve only regular, non-symlink files directly under the configured
   directory, with an extension allow-list: `.js`, `.mjs`, `.cjs`, `.map`,
   `.d.ts`, `.css`. Exclude `.json`, dotfiles, and everything else; the SDK
   needs only JS/declarations/maps.
2. Reject any request whose cleaned path contains a separator, starts with a
   dot, or resolves outside the root. Reject directories (no index, no
   listing). Match suffixes (`.d.ts` is not `path.Ext` == `.ts`).
3. Use `os.Lstat` and refuse `ModeSymlink`; `http.Dir` follows symlinks.
4. `/` static: keep subdirectories (demos need assets) but disable directory
   listings by returning 404 for directories without `index.html`. Reject
   dotfiles.
5. Tests: 404 for `/sdk/package.json`, `/sdk/node_modules/x.js`,
   `/sdk/../secret`, `/sdk/`, and a planted symlink; 200 for
   `/sdk/syncwell.js`.
6. Document that `-sdk` should point at a runtime-only directory in production;
   the Docker image already does this.

### P0-6 — Finish the org rename (F-10)

Files (current-facing only): `.goreleaser.yaml`, `render.yaml`,
`.github/workflows/ci.yml`, `.github/workflows/docs.yml`,
`.github/ISSUE_TEMPLATE/config.yml`, `clients/javascript/package.json`,
`CODE_OF_CONDUCT.md`, `SECURITY.md`, `README.md`, `deploy/systemd/syncwell.service`,
`docs/operations/RELEASE_CHECKLIST.md`, `docs/product/BLOG.md`,
`docs/product/GOVERNANCE.md`, `docs/product/LAUNCH.md`, `website/.vitepress/config.mts`,
`website/index.md`, `website/guide/python.md`, `website/guide/recipes.md`,
`website/project/*`, `website/reference/*`, `website/self-hosting/*`.

1. Replace `Nil-Vaghani`/`nil-vaghani` with `syncwell-labs`.
2. Leave `docs/audits/*` snapshots unchanged; add a one-line "historical" banner
   if needed.
3. **Render is order-dependent:** the pinned digest currently lives under the
   old GHCR owner. Publish `ghcr.io/syncwell-labs/syncwell@sha256:<new>` first
   (P3-1), verify it pulls anonymously, then update `render.yaml` URL *and*
   digest in the same change. Until then, do not touch the image line.
4. Update `.github/workflows/docs.yml` and `ci.yml` defaults:
   `DOCS_ORIGIN=https://syncwell-labs.github.io` (base path stays
   `/syncwell/`; the repo name did not change).
5. Acceptance: run
   `rg -ni 'nil-vaghani' --glob '!docs/audits/**' --glob '!website/node_modules/**' --glob '!website/.vitepress/dist/**'`
   and require no matches. Then `make docs-build`.

### P0-7 — CI truthfulness (F-09)

Files: `.github/workflows/ci.yml`, `.github/workflows/bench.yml`,
`.github/workflows/fuzz.yml` (new), `Makefile`, `.github/dependabot.yml`.

1. Change the `browser-e2e` Make target to run all three suites, and make CI
   call it: kanban, Chromium, Firefox. Resolve binaries with `command -v` in CI
   and export `CHROMEDRIVER_BIN`, `GOOGLE_CHROME_BIN` (or `CHROMIUM_BIN`),
   `FIREFOX_BIN`, and `GECKODRIVER_BIN`; the suites default to `/usr/bin/*`,
   which does not exist for snap-based Chrome on `ubuntu-latest`. Fail loudly
   when a binary is missing.
2. Replace every `uses: owner/action@vN` with a full commit SHA and a trailing
   `# vN` comment (`pinact run` or equivalent). Cover all workflows, not just
   CI.
3. Add `.github/workflows/fuzz.yml` on a weekly schedule plus
   `workflow_dispatch`, matrix over the four existing targets:
   `FuzzCanonicalJSON`, `FuzzValidateInbound`, `FuzzUnmarshalClusterEvent`,
   `FuzzVerifyAccessToken`. Each invocation uses exactly one `-fuzz` target
   (`-run=^$ -fuzz=<name> -fuzztime=60s`).
4. Add a docs link job: internal links offline on PR, full external links
   weekly. Use a pinned action; exclude generated `dist/`.
5. Add a local `make vuln` target that runs `govulncheck ./...` when installed
   and prints install instructions otherwise. CI keeps the authoritative scan.
6. Remove `check-latest: true`; pin the exact Go patch (`1.26.10`) and let
   Dependabot propose bumps. Do not silently float.

---

## 4. Phase P1 — Lifecycle, durability, accountability (Gate A)

| Task | Closes | Size | Depends | Acceptance |
| --- | --- | --- | --- | --- |
| P1-1 Room delete + retention | F-05, F-15 | M | — | Admin can purge a room; retention sweep deletes only opted-in, idle, old logs; delete closes the fd and removes both log and metadata |
| P1-2 Encrypted backups | F-11 | M | — | Backup dir contains no plaintext; wrong identity and tampering fail verification; v1 backup still restores; `make restore-drill` uses encryption |
| P1-3 Audit log | F-12 | M | P1-4 | Evict, purge, backup, restore, and admin auth failures emit one structured audit event with actor/IP/request ID |
| P1-4 Request IDs | F-13 | S | — | `X-Request-ID` is generated or validated, echoed, attached to traces and every request-scoped log line |
| P1-5 Room command send hardening | F-08 | S | — | A room with a full/wedged command queue returns 1013 instead of parking goroutines; no goroutine leak in the test |
| P1-6 Shutdown budget | F-03 | S | P1-7 | `-shutdown-timeout` defaults to 30s; all rooms compact/flush or the shutdown is logged as incomplete |
| P1-7 File-descriptor accounting | F-03, F-15 | S | — | Evicting/deleting a room closes its handle; open log count is observable and tested |

### P1-1 — Room delete, metadata, retention

Store changes (`internal/store/store.go`, new `internal/store/meta.go`):

1. On first append/compact for a room, atomically write
   `<sha256>.meta` containing `{"room": "...", "created": "RFC3339"}` (0600,
   temp + rename + dir sync). This fixes F-15 for operators and makes retention
   able to name rooms without reading every log.
2. Add `Delete(room) error`: close/flush the open handle, remove `.log` and
   `.meta`, sync the directory. Idempotent when absent.
3. Include `.meta` in backups (P1-2). Loading a v1/v2 backup without `.meta`
   must still work; reconstruct metadata on first append.

Server changes:

1. `DELETE /admin/api/rooms/{id}` (or an `action=purge` POST consistent with the
   existing action API) that force-evicts the room, removes persisted state, and
   emits `audit` + `room.purged` webhook.
2. `-retention` / `SYNCWELL_RETENTION` duration, default `0` (off). Sweep on
   startup and then daily. Delete only when: not live, no clients, and both log
   mtime and `.meta.created` are older than the window. No allow-list in v1.
3. `-retention-dry-run` logs what would be deleted; recommended for the first
   week of any deployment.
4. Tests: purge removes files and `Load` returns empty; retention uses an
   injectable clock; a live room is never swept; a dry run deletes nothing.

Do not enable retention by default, and do not delete on idle eviction (that is
the current, non-destructive behavior).

### P1-2 — Encrypted, authenticated backups

Use `filippo.io/age` rather than a hand-rolled AEAD format. It is streaming,
authenticated, and avoids nonce-reuse design mistakes.

Files: `internal/store/store.go`, new `internal/store/backup_crypto.go`,
`cmd/syncwell/main.go`, `tests/e2e/restore_drill.mjs`,
`docs/operations/OPERATIONS.md`, `docs/operations/RESTORE_DRILL.md`.

1. New flags: `-backup-recipient age1...` (repeatable via a custom
   `flag.Value`) and `-restore-identity FILE`. `-backup` with no recipient and no `-backup-plaintext` fails with an
   actionable error.
2. For each room log: encrypt the byte stream to `<name>.log.age`, write to a
   temp file, fsync, rename. Write `manifest.json.age` (encrypted) containing
   format `2`, timestamps, per-file plaintext size, ciphertext size, plaintext
   SHA-256, and ciphertext SHA-256.
3. `VerifyBackup` detects format 1 (plaintext) or 2 (encrypted by magic/name).
   For format 2 it decrypts the manifest, checks every ciphertext hash, and
   decrypts each file to count/verify plaintext hashes; fail on the first
   mismatch. Never write decrypted data into the backup directory.
4. `Restore` decrypts to a temp directory, verifies, then atomically replaces
   the target files. On any failure, leave the existing data untouched.
5. Tests: round trip; wrong identity; one flipped ciphertext byte; truncated
   file; manifest tamper; v1 backup restore; assert no plaintext marker appears
   in any backup file.
6. Document key custody: losing the identity means losing the backup. Recommend
   an offline copy and a documented rotation procedure. `-backup-plaintext`
   prints a warning and is intended only for migration.

Dependency: add `filippo.io/age`. CI's `govulncheck` and Dependabot cover it.

### P1-3 — Audit trail (F-12)

Files: new `internal/server/audit.go`, `internal/server/admin.go`,
`internal/server/hub.go`, `cmd/syncwell/main.go`, `docs/operations/ADMIN_AND_WEBHOOKS.md`.

1. Emit one JSON line per privileged action:
   `{"level":"audit","event":"audit","action":"room.purge","actor":"admin-token|oidc:<sub>","ip":"...","room":"...","result":"ok|denied|error","request_id":"..."}`.
   Never include tokens, secrets, or full state.
2. Actions to cover: server admin auth failure (rate-limited), room evict, room
   purge, resnapshot, and startup with insecure/allow-any-origin flags. CLI
   `-backup`, `-verify-backup`, and `-restore` emit the same event shape to
   stderr and the optional `-audit-log`; they do not traverse the server. The
   `-mint` CLI is offline and does not go through the server either.
3. Optional `-audit-log FILE`: `O_APPEND|O_CREATE|O_WRONLY`, mode 0600,
   line-buffered, `fsync` for admin actions (not for every join). Ship a
   `logrotate` example; do not implement rotation in-process in this task.
4. Actor identity: admin token has no identity, so record `admin-token`
   explicitly and document the limitation. OIDC admins record `sub`.
5. Tests: capture stdout/file, assert one event per action, mode 0600, and no
   secret substrings.

### P1-4 — Request IDs (F-13)

Files: `internal/server/tracing.go`, `internal/server/audit.go`,
`internal/server/hub.go`, tests.

1. Middleware: accept `X-Request-ID` only if it matches
   `^[A-Za-z0-9._-]{8,64}$`; otherwise generate 16 random bytes hex.
2. Put it in the request context, echo it on the response, and add it to the
   server span as `http.request_id`.
3. Add it to `LogEvent` calls that have a request. For room-loop events, carry
   the ID on the command; do not reach into HTTP state from the room goroutine.
4. Never use the request ID as a metric label (unbounded cardinality).

### P1-5 — Non-blocking room command admission (F-08)

Files: `internal/server/server.go`, `internal/server/hub.go`, tests.

1. Add a helper that sends a command with `select` on `room.done` and a
   non-blocking `default`. On a full queue: close the connection with WebSocket
   close code 1013 ("try again later") and log a rate-limited event. Do not
   block the HTTP handler indefinitely.
2. `join` and `leave` get the same treatment. `leave` may be dropped when the
   room is already gone; that is safe. A dropped `join` must close the socket.
3. Preserve ordering: `readPump` starts after the join is accepted, so intents
   cannot overtake the join.
4. Test: construct a room with a stalled loop (or fill the command channel),
   call the send helper, and assert it returns within a bound and emits 1013.
5. Include this path in `make chaos` if practical; a unit test is the minimum.

### P1-6 — Shutdown budget (F-03)

Files: `cmd/syncwell/main.go`, `internal/server/hub.go`, docs.

1. Add `-shutdown-timeout` / `SYNCWELL_SHUTDOWN_TIMEOUT` default 30s, replacing
   the hard-coded 5s.
2. Order: mark not-ready and stop accepting by calling `srv.Shutdown` first
   (WebSockets are hijacked and are not tracked by `http.Server`, so this
   returns without waiting for them), then `hub.Shutdown` (anti-entropy stop,
   room compact/flush), then `store.Close`, then tracing shutdown. Calling
   `hub.Shutdown` first lets new handshakes race a room teardown.
3. If the context expires, log `shutdown_incomplete` with the number of rooms
   not drained. Do not claim a clean shutdown.
4. Test with many persisted rooms and `-fsync=every`; assert the process exits
   without error and a restore/reload sees all acknowledged writes.

### P1-7 — Descriptor accounting (F-03, F-15)

Files: `internal/store/store.go`, `internal/server/hub.go`, metrics tests.

1. Add `FileStore.Stats()` returning `{OpenFiles, OpenLogs, Bytes}` under the
   store mutex. Do not expose per-room maps to the hub.
2. Assert `Compact`, `Delete`, and `Close` remove handles from the maps. Add a
   test that evicts a persisted room and observes `OpenFiles` return to zero.
3. Publish the gauge in P2-1; for now, keep the accessor and unit test.

---

## 5. Phase P2 — Observability and horizontal scale (Gate B/C)

| Task | Closes | Size | Depends | Acceptance |
| --- | --- | --- | --- | --- |
| P2-1 Saturation + cluster metrics and alerts | F-14 | M | P0-4, P1-7 | Dashboard and alerts cover conns, fds, goroutines, heap, relay errors/latency, compaction failures |
| P2-2 Redis Streams bus + lease ownership + re-homing | F-02 | XL | ADR-001, Gate A | Three-node test: kill the owner, a new owner serves the room within the lease TTL; documented RPO/RTO; no room ID labels |
| P2-3 Digest-based anti-entropy | F-02 | M | P2-2 (or relay) | Under 10% packet loss, convergence time and bytes improve versus the full-snapshot baseline |
| P2-4 Scheduled fuzz + soak | F-09 | S | P0-7 | Weekly fuzz and a nightly/soak workflow produce artifacts; failures are actionable |
| P2-5 Docs/SLO refresh | F-14 | S | P2-1 | SLOs reference real metric names; runbooks match flags and failure modes |

### P2-1 — Metrics and alerts

Add exactly these, with bounded labels (no room IDs, no client IDs, no keys):

- `syncwell_connections_current`, `syncwell_connections_limit`,
  `syncwell_connection_rejects_total`
- `syncwell_open_log_files`, `syncwell_open_log_bytes`
- `syncwell_process_open_fds`, `syncwell_process_fd_limit` (Linux
  `/proc/self/fd`; report 0 on other platforms)
- `syncwell_goroutines`, `syncwell_heap_bytes` (or rely on
  `go_goroutines`/`go_memstats_heap_alloc_bytes`; do not duplicate if the Go
  collector already provides them)
- `syncwell_cluster_relay_duration_seconds{peer}` (histogram),
  `syncwell_cluster_relay_errors_total{peer,reason}`,
  `syncwell_cluster_peer_up{peer}`
- `syncwell_anti_entropy_bytes_total`, `syncwell_compaction_failures_total`,
  `syncwell_token_in_query_total`

Update `deploy/monitoring/prometheus` rules and the Grafana dashboard. Alerts:
fd/limit > 80%, heap vs `GOMEMLIMIT`, connection rejects sustained, relay error
rate, compaction failures, peer down. Set `GOMEMLIMIT` guidance in docs; do not
hard-code it in the binary.

### P2-2 — Redis Streams bus, ownership, re-homing (XL, ADR first)

This is the long pole and is **not** release-blocking for single-node GA.
Record technical review and maintainer acceptance in
`docs/adr/ADR-001-cluster-transport.md` before calling the mode production-ready.

Proposed design:

1. Modes: `-cluster-mode=relay|redis` (default `relay`). `redis` requires
   `-redis-url` (redacted in logs) and `-node-advertise id=wss://host`.
2. Event log: one stream per room, `sw:{roomhash}:events`, with the room hash
   in a Redis Cluster hash tag. Never store the raw room ID in keys or event
   payloads. Owners replay with ordered `XRANGE` after the snapshot offset; a
   consumer group is not required. Snapshot exact offsets before trimming with
   `XTRIM MINID`; do not trim by approximate maximum length on every append.
3. Ownership: atomically acquire a fenced lease for 15s, renewed every 5s.
   Routing must be explicit: browsers cannot read headers on a failed
   WebSocket handshake. Add `GET /route?room=<id>` returning
   `{"node":"...","url":"wss://..."}`; the SDK's opt-in `followOwner` calls it
   before connecting. Non-SDK clients still get HTTP 421 with
   `X-Syncwell-Owner` plus a `Location` header, but do not rely on header
   visibility for browser clients.
4. State: periodic snapshot at `sw:{roomhash}:snapshot` (compacted CRDT,
   bounded) plus `XRANGE` after the snapshot offset for replay. Redis must run
   with AOF `appendfsync everysec`; startup verifies both settings. RPO/RTO
   claims are conditional on the selected provider's replication/failover
   guarantees and measured recovery results.
5. Re-homing: lease expiry -> `SET NX` election -> winner loads snapshot,
   replays the stream, then accepts clients. Clients reconnect to the advertised
   URL; add an opt-in `followOwner` behavior to the SDKs (default off to avoid
   surprising redirects).
6. Tests: CI `services: redis:7-alpine`; three processes; kill -9 the owner;
   assert convergence and no acknowledged-write loss beyond the documented RPO.
   Unit-test the lease and offset logic with a fake clock.
7. Rollback: `-cluster-mode=relay` remains the default and stays supported. No
   hard dependency on Redis in the binary; keep it behind a build tag only if
   the dependency footprint is a problem, otherwise link it normally.

Do not implement re-homing without shared state. If the ADR rejects Redis, the
fallback is the documented single-node/full-mesh ceiling, not a half-working
ownership layer.

### P2-3 — Digest anti-entropy

Replace the 30s full-snapshot push with a per-room digest (hash of
`key -> (ts,node,valueHash)`), exchange digests, and send only missing keys via
the existing `hello`/delta machinery. Bound digest size; paginate large rooms.
Measure bytes and convergence time before/after; keep the full snapshot as a
fallback when a digest is rejected.

### P2-4 — Scheduled fuzz and soak

Weekly fuzz matrix (P0-7). Nightly (or weekly) soak:
`CLIENTS=2000 DURATION_MS=600000 make bench` on a dedicated runner, plus
`cmd/loadtest` with `-max-conns` set. Publish artifacts; do not block PRs on
multi-minute runs.

---

## 6. Phase P3 — Release and migration (after Gate A)

| Task | Closes | Size | Depends | Acceptance |
| --- | --- | --- | --- | --- |
| P3-1 Release `0.5.0` | D8 | M | Gate A+B | Tagged release, signed image, SBOM, npm provenance, notes |
| P3-2 Migration guide | D2, D5, D6 | S | P3-1 | Guide covers token default, backup encryption, TLS floor, `-max-conns`, org/render; v1 backup read path |
| P3-3 Add `CHANGELOG.md` | D8 | S | — | Entries for every behavior change, with "action required" markers |
| P3-4 Owner-only: demo, publish, launch | Phase 7 | S | P3-1 | Live demo on the new owner, npm/GHCR published, launch post |

**Migration order (do not reorder):**

1. Publish `ghcr.io/syncwell-labs/syncwell@sha256:<digest>` and verify an
   anonymous pull.
2. Update `render.yaml` URL + digest; deploy the demo.
3. Update docs/CI origins and all remaining old-org links.
4. Publish npm `syncwell-client@0.5.0` with the new token default.
5. Announce the breaking defaults.

---

## 7. Verification matrix

Run before every release candidate; CI must run the same commands.

```bash
make all
go test -race ./...
make cover-check
make test-python
make chaos
make restore-drill
make container-smoke
make docs-build
make a11y
make browser-e2e            # after P0-7: kanban + chromium + firefox
(cd clients/javascript && npm ci && npm test && npm run typecheck && npm run build)

# One fuzz target per invocation (Go requirement):
go test ./internal/server -run=^$ -fuzz=FuzzValidateInbound -fuzztime=60s
go test ./internal/server -run=^$ -fuzz=FuzzUnmarshalClusterEvent -fuzztime=60s
go test ./internal/server -run=^$ -fuzz=FuzzVerifyAccessToken -fuzztime=60s
go test ./internal/crdt  -run=^$ -fuzz=FuzzCanonicalJSON -fuzztime=60s

# If installed locally; CI is authoritative:
govulncheck ./...

# Local evidence for the owner-only phases (never publishes or signs):
make workflows
make release-dry-run
make redis-drill
make soak-quick
```

Targeted checks that must exist as tests (not manual):

- TLS 1.2 rejection by default and acceptance with `-tls-min=1.2`.
- No `token=` in default SDK URLs; both carriers authenticate.
- Cluster 307/308 target is never contacted.
- `-max-conns=1` rejects the second socket and frees the slot.
- `/sdk/` traversal, dotfile, directory, and symlink requests return 404.
- Purge deletes log + meta and closes the fd; retention dry-run deletes nothing.
- Encrypted backup has no plaintext, rejects wrong identity/tamper, reads v1.
- Audit event per privileged action; file mode 0600.
- `X-Request-ID` echo and validation.
- Wedged room returns 1013; shutdown reports incomplete drains.
- Redis: owner kill -> re-home within lease TTL (P2-2).

---

## 8. Risks and rollback

| Risk | Mitigation | Rollback |
| --- | --- | --- |
| SDK token default breaks old servers | Server accepts query/subprotocol/header; docs state the server requirement; CHANGELOG | Ship a patch releasing the old default, or set `tokenTransport:"query"` |
| TLS 1.3-only breaks legacy clients | `-tls-min=1.2` escape hatch | Restart with the flag |
| Retention/purge deletes data | Off by default; purge requires explicit action; audit; `-retention-dry-run` | Restore from the last encrypted backup |
| Backup identity lost | Document custody; keep `-backup-plaintext` migration path | Recover identity from escrow; otherwise unrecoverable by design |
| `-max-conns` blocks load tests | Update `cmd/loadtest` and docs; default is generous | Raise or set `0` |
| Redis re-homing loses writes | AOF, lease TTL, tests; documented RPO/RTO | Return to `-cluster-mode=relay` |
| Render image 404 after org rename | Publish and verify the new digest before editing `render.yaml` | Revert the one-line image change |
| Pinact SHA churn | One mechanical PR; Dependabot keeps pins fresh | Revert the workflow file |

---

## 9. Definition of done

- [ ] All P0 and P1 tasks closed with the tests listed above.
- [ ] P2-1, P2-4, P2-5 closed; P2-2 and P2-3 shipped or explicitly deferred by
      a signed ADR (single-node GA may ship without them).
- [ ] `rg -i 'nil-vaghani'` clean outside historical audits.
- [ ] `rg 'token=' clients/javascript/syncwell.js clients/python/syncwell_client.py`
      shows only explicit opt-in paths.
- [ ] `CHANGELOG.md` and the migration guide list every behavior change.
- [ ] `/metrics` exposes the saturation and cluster metrics; alerts reference
      real names.
- [ ] A release candidate passes the full verification matrix from a clean
      checkout.
- [ ] Owner-only Phase 7 work is either done or clearly marked as the only
      remaining gap.



## Progress

### Completed

- **P0-1 TLS floor**: Added `-tls-min` flag (1.2/1.3), parser function `parseTLSMinVersion`, updated startup log to show configured minimum; docs updated (DEPLOY.md, quickstart.md, configuration.md, THREAT_MODEL.md). Verification: `go test ./cmd/syncwell` covers parsing and TLS 1.2 rejection by default/acceptance with opt-in.

- **P0-2 Token carrier defaults**: JS defaults to WebSocket subprotocol; Python defaults to `Authorization: Bearer`; query token use is opt-in. The Python raw-socket handshake validates URL authority/request-target and header names/values before writing them, preventing caller-provided URL or token values from injecting handshake data. Added `syncwell_token_in_query_total` and rate-limited `ws_token_query_deprecated` logging without token values. SDK carrier examples are documented; bundled-demo query URLs are explicitly labeled demo-only in the README, getting-started, quickstart, deployment, and launch docs. Added the breaking default change to CHANGELOG. Verification: JS tests, Python real-server E2E, and server tests pass.
- **P0-3 Cluster redirect guard**: Relay now uses a dedicated no-proxy HTTP transport with a two-second dial/client timeout and TLS 1.2 floor, and refuses redirects. Cluster peer URLs are validated as absolute scheme/host-only URLs with no credentials, path, query, or fragment; plaintext HTTP requires `-cluster-insecure`. Verification: `go test ./internal/server ./cmd/syncwell`, including 307/308 target-isolation and error-counter checks.
- **P0-4 Global connection budget**: Added `MaxConns` and `-max-conns` / `SYNCWELL_MAX_CONNS` (default 4096, 0 = unlimited); connections are admitted before upgrade, rejected with 503 and `Retry-After: 5`, and released once either WebSocket pump exits. Added current/limit/reject metrics. Updated the 10k load-test server command and documented the process-wide and per-room limits. Verification: `go test -race ./internal/server ./cmd/syncwell`.
- **P0-5 Safe static/SDK serving**: Implemented SDK extension allow-list (.js, .mjs, .cjs, .map, .d.ts, .css), symlink/dotfile/directory denial, root static symlink denial, and a pre-ServeMux SDK traversal guard. Added static FS tests for allow-list, symlink, listing, and traversal behavior. Verification: `go test ./internal/server ./cmd/syncwell`.
- **P1-4 Request IDs**: Middleware accepts only 8–64 safe characters, otherwise generates 16 random bytes as hex; it echoes the ID, stores it in context, and adds `http.request_id` to server spans. Request-scoped logs and the resnapshot/evict room commands carry the ID. Verification: tracing tests cover validation, echo, and span attributes; log tests cover request ID propagation.
- **P1-3 Audit trail**: Added JSON audit events to stderr and optional append-only mode-0600 `-audit-log` file with fsync per event. Covered rate-limited admin auth failures, resnapshot, eviction, purge, unsafe startup options, and CLI backup/verify/restore. Added logrotate example and documented actor identity. Verification: server and CLI tests check event fields, no credentials, request IDs, failure throttling, file mode, and command events.
- **P1-1 Room delete + retention**: Added atomic mode-0600 room metadata sidecars, idempotent store deletion, metadata-aware plaintext backups/restores, authenticated admin purge with audit/webhook, opt-in startup/daily retention, injectable sweep time, dry-run events, and connected-client protection. Verification: targeted store/server/CLI tests and `git diff --check` pass.
- **P1-2 Encrypted backups**: Added age-encrypted logs, metadata, and manifest; hash/authentication verification; staged restore; repeatable recipients; identity-based verify/restore; explicit warned plaintext migration mode; and v1 plaintext restore compatibility. Both encrypted and plaintext backup/restore reject source/store path overlap and empty paths. Encrypted backup refuses symlinks/non-regular entries, confirms opened files match the enumerated files, and hashes plaintext in the same pass it encrypts. Plaintext backup copies each file and its manifest through synced random sibling temps before replacing them. Manifest verification accepts only unique `.log.age` and `.meta.age` entries. Both restore paths stage and hash-check source files before changing live files; encrypted restore also rechecks plaintext while decrypting, closing the verify/decrypt time-of-check gap. Encrypted backup/restore retain the previous destination until the staged directory rename is synced, require an existing destination to be a real directory, and attempt rollback if that sync fails. Plain restore rolls back replaced logs/metadata on errors and preserves its staging directory if rollback itself fails. Updated the automated restore drill and key-custody docs. Verification: encrypted round-trip/wrong-identity/tamper tests, `go test ./internal/store`, and `make restore-drill` pass.
- **P1-5 Room command admission**: WebSocket join/op/merge/hello/presence/ack/leave admission is non-blocking; saturated rooms close clients with 1013; full-queue events are rate-limited. Verification: saturated queue tests check prompt rejection, 1013, and read-pump exit.
- **P1-6 Shutdown budget**: Added 30s `-shutdown-timeout` / `SYNCWELL_SHUTDOWN_TIMEOUT`, server-before-hub ordering, final room compaction, and incomplete-room counts. Timeout logging now covers anti-entropy drain, command admission, and room completion waits; a table-driven test covers all three timeout paths and their room counts. Verification: `go test ./internal/server -run TestHubShutdown` passes.
- **P1-7 Descriptor accounting**: Added `FileStore.Stats()`, descriptor close on idle/admin eviction and room deletion, and open-log metrics. Verification: store reopen/close stats and persisted-room eviction tests pass.
- **P2-1 Saturation + cluster metrics**: Added aggregate-only metrics with no room/client/key labels for process FDs/limit, goroutines, heap/GOMEMLIMIT, relay per-peer latency and bounded reasons, anti-entropy bytes, compaction failures, connection/command rejects, and open logs. Updated Prometheus alerts, Grafana dashboard, and `ALERTS.md`, including GOMEMLIMIT guidance. Verification: metrics and relay-label tests, Go tests, dashboard JSON parse, and `git diff --check` pass.
- **P2-5 Docs/SLO refresh**: Added an SLO table based on actual metric names and runbooks for connection, FD, heap, persistence, shutdown, queue, retention, and relay failures.
- **P2-3 Digest anti-entropy**: Added authenticated per-register digests, bidirectional missing/different-register exchange, delta ingestion and client resnapshot. Pages contain up to 128 registers, each request/response is capped at 1 MiB, and oversized exchanges use full-snapshot fallback split into event bodies under 1 MiB. Tests cover 300-key pagination, peer-only register delivery, single-register delta bytes, and chunked fallback for a >1 MiB room snapshot. A paired 10-round test drops one event (10%) and measures full-snapshot vs delta bytes and convergence time, including local store persistence; with modeled 100 MB/s transfer delay, a repeated race run measured 2.64 MB / 1.07–1.81s for snapshots vs 236 KB / 0.33–0.64s for deltas.
- **P3-2 Migration guide**: Added the required token/TLS/encrypted-backup/v1 restore/connection/static-serving/org cutover steps and expanded it to cover 1013 reconnects, shutdown drains, audit files, request IDs, token-query deprecation metrics, cluster-peer URL/transport rules, metadata, and operations metrics. `make docs-build` passes.
- **P3-3 Changelog**: Added action-required notes for breaking token/TLS/backup/metrics/capacity/static/shutdown/cluster changes and summarized security, lifecycle, monitoring, CI, and ownership changes. Final versioned release notes still await the release gates.
- **Coverage gate fix**: `internal/store` was below its 75% floor (71.8%) after the backup/restore hardening. Added `internal/store/rollback_edges_test.go` covering rollback and staged-replacement paths (`rollbackDirReplacement`, `rollbackPlainRestore`, `replaceStagedDir`), `openRegular` symlink/directory refusal, `copyVerified` mismatches, overlap helpers, and the `Backup`/`Restore` early guards. `make cover-check` now passes (`internal/store` 75.6% >= 75%).
- **Review pass fixes**: Reverted the `render.yaml` image owner change that pointed at an unpublished digest (Render could not pull it) and documented the P3-1 cutover dependency; added the missing `SYNCWELL_TLS_MIN` row to `website/self-hosting/configuration.md`; made the hazard-safe default of the internal JS `buildURL` helper keep tokens out of the URL; and made `NoDirListFS` reject a symlinked static root. Verification: `go test ./internal/server ./cmd/syncwell`, JS tests/typecheck/build, and `make docs-build` pass.
- **Local evidence tooling**: Added `make workflows` (offline YAML parse, SHA-pinned actions, exact Go toolchains, optional actionlint), `make release-dry-run` (six-target snapshot build, checksums, optional SBOM and signing readiness, never publishes or signs), `make redis-drill` (throwaway AOF Redis plus the three-node SIGKILL/re-home and relay-rollback drill, writing RPO/RTO evidence), and `make soak`/`make soak-quick` (the nightly soak workload with bench, loadtest, server log, and metadata artifacts). Verification on 2026-11-04: the workflow gate passed on 16 YAML files; the dry run produced 6/6 cross-builds with checksums; the Redis drill passed with 0 acknowledged writes lost and a 15.6 s RTO; the quick soak completed 200 clients with 0 failed connects. These are local equivalents only; hosted runner execution, provider fencing, signing/publishing, and maintainer acceptance remain owner work.

### Full local verification (2026-10-29)

Ran the complete matrix on the working tree and all gates are green:

`make all` (fmt-check, vet, `go test -race ./...`, cover-check, JS tests, e2e, persist, restore-drill, gc, multi-client, chaos, build) · Python E2E (3 tests) · SDK typecheck/build (79 tests) · docs build · `make browser-e2e` (kanban + Chromium + Firefox) · a11y (30 runs, 0 violations) · all four 60-second fuzz targets · `make vuln` (0 affecting vulnerabilities) · Redis three-process recovery + relay rollback E2E · container smoke.

### In Progress

- **P0-7 CI truthfulness**: Browser suites, SHA-pinned action references, scheduled fuzz and docs-link workflows, exact Go patch pinning, and `make vuln` are implemented. Local verification on 2026-10-29 passed `make all`, Python E2E (3 tests), SDK install/tests/typecheck/build (79 tests), docs build, all browser suites, a11y (30 runs, 0 violations), all four required 60-second fuzz targets, `make vuln` (0 reachable vulnerabilities), Redis recovery + relay rollback E2E, container smoke, and `go test -race ./...` after the Redis fake-store tests. All 10 workflow YAML files parse and every action reference is SHA-pinned with a version comment; no `check-latest` remains. GitHub Actions execution is still pending because these workspace changes have not been pushed; the latest CI run visible on `main` is 2026-10-22, and the remote workflow list does not yet contain the new docs-link/fuzz/soak workflows.
- **P0-6 Org rename**: Current-facing source links, package metadata, governance, docs configuration, and systemd metadata now point to `syncwell-labs`; the stale `dist/config.yaml` release artifact (untracked, gitignored) was also updated. The new owner image is not yet published: anonymous `docker manifest inspect ghcr.io/syncwell-labs/syncwell@sha256:f37fc729...` fails while the same digest under the old owner resolves. `render.yaml` therefore intentionally keeps the working old-owner digest until P3-1 publishes and anonymously verifies the replacement; switching the owner early makes Render unable to pull the image. The old-owner search returns only this one deployment pin plus `docs/audits/` history; remove the pin in the same change that publishes v0.5.0, then rerun the search.
- **P2-2**: Runtime, SDK routing, bounded metrics, fake-clock/fake-store lease and replay unit tests, and local Redis 7 three-process recovery/rollback validation are implemented and reproducible with `make redis-drill`, which starts its own AOF Redis and writes RPO/RTO evidence. The Redis 7.4 drill killed `node-a` during acknowledged writes; `node-b` acquired after lease expiry and replayed acknowledged values in 15.6 seconds. The drill then restarted all three nodes in relay mode and verified a write converged to a third peer. Room IDs are SHA-256 hashed in Redis keys, routine logs, audit logs, and traces; stream payloads omit the room ID. Startup requires AOF `appendonly=yes` and `appendfsync=everysec`. Remaining acceptance: hosted GitHub Actions execution, provider failover fencing/RPO evidence, and maintainer/provider acceptance. Do not claim production re-homing support until all are demonstrated.
- **P2-4 Scheduled fuzz + soak**: The weekly four-target fuzz workflow and nightly dedicated-runner 2,000-client bench/loadtest soak with uploaded artifacts are implemented. All four fuzz targets passed locally for 60 seconds each. GitHub currently reports zero self-hosted runners for this repository, so the first scheduled soak artifact still needs the `syncwell-soak` runner registered; `make soak` reproduces the workload and artifacts locally but does not replace the dedicated-runner 10-minute soak.
- **P3-1/P3-4**: Release checklist is prepared for v0.5.0, including signed-image verification, anonymous pull, digest capture, and SDK tag matching. Versioned release, package/image publication, hosted demo, and announcement remain release-owner work after all gates pass. GitHub currently reports `syncwell-labs/syncwell` as a private repository; public source links, Pages availability, and release visibility therefore need an owner decision before launch.

### Next Steps

1. All local implementation edits and the full local verification matrix are complete and green (see "Full local verification" above). Refresh local evidence with `make workflows`, `make release-dry-run`, `make redis-drill`, and `make soak`; none of these close the owner-only items below.
2. A repository maintainer must publish/open the change and confirm P0-7 CI, Redis three-process recovery/rollback, and docs-link workflows in GitHub Actions.
3. Register the dedicated `syncwell-soak` runner and capture its first 2,000-client soak artifact. Provider failover fencing/RPO evidence and maintainer acceptance are required before calling Redis re-homing production-ready.
4. After Gate A+B pass, the release owner must confirm repository visibility and complete v0.5.0 publication. Publish and anonymously verify the replacement GHCR image before updating Render for P0-6; then run the final old-owner search and public-link verification.

**Gate status**: A (single-node GA) is close: P0-1..5 and P1 are implemented and locally verified; P0-6/7 external cutover/CI confirmation remain. B (operable): P2-1 and P2-5 are implemented; P2-4 needs its first dedicated-runner artifact. C (horizontal): P2-3 relay anti-entropy is implemented and measured; P2-2 has opt-in runtime, SDK routing and a local three-process recovery drill, with hosted CI/provider/rollback validation still open. `0.5.0` ships A + B only after their remaining gates pass.

**Definition of done** (see Section 9 of the plan): all P0/P1 tasks closed with tests; `rg 'Nil-Vaghani'` clean outside historical audits; `rg 'token=' clients/javascript/syncwell.js clients/python/syncwell_client.py` shows only explicit opt-in paths; `CHANGELOG.md` and the migration guide list every behavior change; `/metrics` exposes the saturation and cluster metrics; alerts reference real names; a release candidate passes the full verification matrix from a clean checkout; Owner-only Phase 7 work is either done or clearly marked as the only remaining gap.

**Owner-only**: Hosted demo, npm/GHCR publish, launch post, render digest update.

## 10. References

- OWASP ASVS 5.0 (authentication, session management, validation,
  communications, file/resource handling, configuration).
- OWASP WebSocket Security Cheat Sheet; OWASP SSRF Prevention Cheat Sheet.
- RFC 6455 (WebSocket), RFC 6750 (Bearer tokens), RFC 7519 (JWT),
  RFC 8725 (JWT BCP), RFC 9700 (OAuth 2.0 Security BCP), RFC 8446 (TLS 1.3),
  RFC 9325 (TLS recommendations).
- Prometheus instrumentation best practices (bounded labels, `_total` suffixes).
- SLSA v1.0 and Sigstore/cosign (already used for image signing/provenance).
- Go `net/http` production guidance (timeouts, `MaxHeaderBytes`, hijacked
  connections) and `GOMEMLIMIT`.
- CWE-522/CWE-598 (credentials in URLs), CWE-400/CWE-770 (resource
  exhaustion), CWE-918 (SSRF).
