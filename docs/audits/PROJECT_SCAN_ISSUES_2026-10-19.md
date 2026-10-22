# Syncwell — Full Project Scan: Issues, Mistakes & Fix Plan

**Scan date:** 2026-10-19
**Scope:** canonical monorepo (`/home/useer/E/Syncwell`) — Go server (`cmd/`, `internal/`),
JS SDK (`clients/javascript/`), demos (`examples/demos/`), tests (`tests/`), build/CI (`Makefile`, `Dockerfile`,
`.github/`), docs (`docs/`), and the embedded split repos (`syncwell-engine/`,
`syncwell-public/`).
**Method:** static read of every source file + `go build`, `go vet`, `go test ./...`,
`node --test`, `gofmt -l`, `git status/diff`, and live runs of the E2E harnesses against
the built binary.

> **ટૂંકો સાર (Gujarati):** પ્રોજેક્ટનું architecture સારું છે, પણ અત્યારે **build અને E2E
> લાલ (red) છે** — 1 JS unit test fail, 2 E2E convergence test fail, 4 Go files
> unformatted, અને CI `main` branch પર trigger થાય છે જ્યારે repo `master` પર છે. ઉપરાંત
> cluster replication, room lifecycle, GC low-water, persistence filename limits, અને
> nested git repos જેવા ગંભીર મુદ્દા છે. નીચે દરેક issue માટે evidence, impact, ને
> "પોતાને પૂછેલા પ્રશ્નો + fix" આપ્યા છે.

---

## 0. Verification results (raw evidence)

| Check | Command | Result |
| --- | --- | --- |
| Go build | `go build ./...` | ✅ pass |
| Go vet | `go vet ./...` | ✅ pass |
| Go tests | `go test ./...` | ✅ pass (`crdt`, `sequence`, `server`, `store`) |
| JS tests | `cd clients/javascript && node --test` | ❌ **17 pass / 1 fail** — `outbound frames carry the current protocol version` |
| Offline E2E | `node tests/e2e/offline_e2e.mjs` | ❌ **fail (3/3 runs)** — `A must receive B's offline edit after reconnect` |
| Multi-client E2E | `node tests/e2e/multi_client_e2e.mjs` | ❌ **fail (3/3 runs)** — `Last writer should win on concurrent set` |
| Persist E2E | `node tests/e2e/persist_e2e.mjs` | ✅ pass |
| GC E2E | `node tests/e2e/gc_e2e.mjs` | ✅ pass |
| Formatting | `gofmt -l .` | ❌ 4 files: `internal/server/{auth,bus,hub,server}.go` |
| Whitespace | `git diff --check` | ✅ clean |
| Binary size | `ls -la bin/syncwell` | 10,537,485 B (**~10.5 MB**; README says "~8 MB") |

**Bottom line:** `make all` (vet + test + test-js + e2e + persist + gc + multi-client + build)
is **red right now** because of the JS test and the two E2E tests. CI is additionally
**not triggered on push** to the real default branch.

---

## Severity index

| ID | Severity | Area | One-line |
| --- | --- | --- | --- |
| B1 | 🔴 Blocker | Tests/SDK | `clients/javascript/syncwell.test.js` fails → `make test-js`, `make all` red |
| B2 | 🔴 Blocker | Tests/E2E | `offline_e2e` + `multi_client_e2e` fail deterministically |
| B3 | 🟠 High | Build hygiene | 4 Go files not `gofmt`-clean; CI has no format gate |
| B4 | 🟠 High | CI | Workflow triggers on `main`, default branch is `master` |
| H1 | 🔴 High | Correctness | Cluster ops not persisted / clock not advanced; remote `snapshot` uses destructive `Load` |
| H2 | 🟠 High | Cluster | Replication path is unwired, unauthenticated, and can stall a room |
| H3 | 🟠 High | Memory | `h.rooms` never evicts empty rooms → unbounded leak |
| H4 | 🟠 High | Concurrency | Room creation does disk I/O while holding the global hub mutex |
| H5 | 🟠 High | Correctness/Sec | GC low-water trusts client-reported clock → data-loss vector |
| H6 | 🟠 High | Storage | base64 room filename can exceed `NAME_MAX` → persistence fails |
| M1 | 🟡 Medium | Security | `admin_token` accepted as URL query → leaks to logs/proxies |
| M2 | 🟡 Medium | Security | Client-chosen `TS.Node` is not bound to the connection |
| M3 | 🟡 Medium | Correctness | Go vs JS tie-break can diverge; no cross-language conformance tests |
| M4 | 🟡 Medium | Robustness | `merge` persistence failure emits N error frames + snapshot |
| M5 | 🟡 Medium | Storage | `Load` skips **all** malformed lines, hiding corruption |
| M6 | 🟡 Medium | Dead code | `membership.go` routes/registry never wired |
| M7 | 🟡 Medium | Coverage | `make all` omits chaos/bench/browser-e2e; server HTTP/WS untested |
| L1 | 🔵 Low | Docs | "100% complete / production numbers" claims vs. red tests; size drift |
| L2 | 🔵 Low | Repo hygiene | `syncwell-engine/`, `syncwell-public/` embedded as untracked git repos |
| L3 | 🔵 Low | Supply chain | Public repo missing `go.sum`; Docker `ARG GITHUB_TOKEN` persists a secret in a layer |

---

## BLOCKERS

### B1 — SDK unit test fails (`make test-js` / `make all` red)

**Evidence**
- `clients/javascript/syncwell.test.js:85-93` calls `sw.connect(); sw.set("title","hello");` then asserts
  the last frame sent has `protocol`, but **no frame is sent**.
- `clients/javascript/syncwell.js:150-154` — `_send()` only transmits when `this._connected() && this._ready`:
  ```js
  _send(obj) {
    if (this._connected() && this._ready) {   // _ready is false until first snapshot
      this.ws.send(JSON.stringify({ ...obj, protocol: protocolVersion, epoch: this.epoch }));
    }
  }
  ```
- `_ready` is only set to `true` in `_recv()` when the `snapshot` frame arrives
  (`clients/javascript/syncwell.js:213-226`). The test never feeds a snapshot, so `sent` is empty and
  `sent.at(-1)` is `undefined` → `TypeError`.

**Impact:** `make test-js` and therefore `make all` and the CI job fail. The build is red.

**Self-questions**
- *Is the product broken, or the test?* The product is intentionally correct: writes made
  before the server's snapshot are applied locally and shipped via `merge` on join
  (`_localWrite` → `_saveLocal`; snapshot handler sends full state). The test simply omits
  the snapshot handshake.
- *Should `_send` drop early ops?* Gating on `_ready` avoids sending before the room
  handshake; keeping that is safer. So fix the **test**, not the gate.

**Fix:** In `clients/javascript/syncwell.test.js`, simulate a `snapshot` before `set()` (or assert on the
`merge`/`ack` frames instead), e.g.:
```js
sw.connect();
FakeWebSocket.last.onmessage({ data: JSON.stringify({ protocol: 1, type: "snapshot", epoch: 1, state: {} }) });
sw.set("title", "hello");
```
**Verify:** `cd clients/javascript && node --test` → all pass.

---

### B2 — Offline and multi-client E2E tests fail deterministically

**Evidence**
- `node tests/e2e/offline_e2e.mjs` → `A must receive B's offline edit after reconnect` (3/3).
- `node tests/e2e/multi_client_e2e.mjs` → `Last writer should win on concurrent set` (3/3).
- A minimal re-run with the **same SDK and binary**, but replacing the harness's
  `waitHealthy()` with a plain `await sleep(500)`, **passes**. Using `http.get` for the
  health probe also passes. Only the `fetch`-based harness wins/overshoots.
- Root-cause trace: after a `close()` + reconnect, the new `WebSocket` created by
  `clients/javascript/syncwell.js:109` stays in `readyState === 0` (CONNECTING) forever, even though the
  Go server logs the new `ws_join` (`{"client":"c3",...}`). A `await sleep(0)` after
  `waitHealthy()` was enough to make it reliably pass — i.e. a Node/undici
  `fetch` → `WebSocket` handshake race, not a server bug.

**Impact:** `make e2e`, `make test-multi-client`, `make all`, and CI are red. The
actual Phase-2.1 offline-convergence guarantee *does* work (verified manually with
`/tmp/dbg2.mjs`: A ends with `{"k":"B-edit","offline-note":"..."}`), but the shipped proof
is unreliable.

**Self-questions**
- *Is offline convergence really broken?* No. An instrumented replica of the exact test
  (server logs + per-client `change` events) shows both replicas converge. The failure is
  the harness's readiness probe racing Node's built-in WebSocket.
- *Why does only `fetch` trigger it?* Node's global `WebSocket` and `fetch` share the
  undici dispatcher; a fetch connection established immediately before creating sockets
  appears to leave the pool in a state where a later handshake never settles. Any tiny
  yield (or `http.get`, or a settle sleep) avoids it.
- *Will it happen in a real browser?* No. `fetch` and `WebSocket` are independent in the
  browser; this is Node-test-specific. But CI runs Node, so it must be fixed.

**Fix (pick one, all robust):**
1. Make `waitHealthy()` consume the body and yield before returning:
   ```js
   const r = await fetch(`http://localhost:${PORT}/healthz`);
   await r.text();
   await new Promise((res) => setTimeout(res, 0)); // let undici settle
   if (r.ok) return;
   ```
2. Or drop `fetch` for the probe and use `node:http` (`http.get` verified to work).
3. Or use the `ws` npm package instead of Node's global `WebSocket` in tests.
Apply the same helper to `offline_e2e.mjs`, `multi_client_e2e.mjs`, and — defensively —
`persist_e2e.mjs`, `gc_e2e.mjs`, `bench.mjs`, `container_smoke.mjs` (all share the pattern).

**Verify:** `make e2e && make test-multi-client` pass repeatedly (run 5×).

---

### B3 — Go source is not `gofmt`-clean, and CI has no format gate

**Evidence**
- `gofmt -l .` → `internal/server/auth.go`, `internal/server/bus.go`,
  `internal/server/hub.go`, `internal/server/server.go` (import ordering, struct/field
  alignment — e.g. `hub.go:89-96`, `hub.go:460-477`, `auth.go:37-46`).
- `Makefile` has `fmt` (writes) but `vet`/`all`/CI never check formatting.

**Impact:** inconsistent style; the repo can drift without anyone noticing. `go vet` passes,
so this is not a red build, but it violates the stated convention ("gofmt" in `make all`
history) and makes diffs noisy.

**Self-questions**
- *Why didn't the previous "gofmt" commit fix these?* Those files are in the current
  uncommitted change set (`git status` shows `M internal/server/{auth,hub,server}.go` and
  untracked `bus.go`), so they were edited after the last `gofmt` run.

**Fix:** `make fmt` (i.e. `gofmt -w .`), then add a CI/Make gate:
```make
fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo "run 'make fmt'"; exit 1)
```
and call it from `vet`/`all` and CI.

**Verify:** `gofmt -l .` prints nothing.

---

### B4 — CI triggers on `main`, but the default branch is `master`

**Evidence**
- `.github/workflows/ci.yml:3-5`:
  ```yaml
  on:
    push:
      branches: [main]
  ```
- `git branch --show-current` → `master`.

**Impact:** Every push to the real default branch **skips CI entirely**. The README badge
and the PLAN's "CI green" claim cannot be true as configured. This is likely why the red
tests above were not caught.

**Self-questions**
- *Rename branch or fix workflow?* Cheapest and least disruptive: allow both. Or branch
  rename + update remote. Either way, PRs are still covered (`pull_request:`).

**Fix:** `branches: [main, master]` (or drop the filter and let all branches run).

**Verify:** push to `master`, confirm the workflow runs.

---

## HIGH

### H1 — Cluster replication loses writes and can clobber state

**Evidence**
- `internal/server/hub.go:371-392` `applyCluster()`:
  ```go
  case "op":
      r.doc.ApplyRegister(ev.Key, *ev.Register)     // no persist, no clock.Observe, no epoch/dedup
  case "snapshot":
      r.doc.Load(ev.State)                          // destructive replace, not a CRDT merge
  ```
- Contrast with the local path `applyAndPersist` (`hub.go:174-199`) which calls
  `r.clock.Observe`, `r.store.Append`, and merge semantics.
- `newRoom` startup load uses `Load` on a *fresh* doc (`hub.go:162`) — that one is fine;
  `applyCluster` reusing `Load` on a live doc is not.

**Impact**
- Ops received from a peer are never persisted → after a node restart, cross-node writes
  vanish.
- The clock is never advanced on cluster ops → local timestamps can later be lower than
  already-applied remote state.
- A remote `snapshot` **replaces** the whole document, discarding any concurrent local
  writes made while that snapshot was in flight → divergence/data loss (violates the
  project's core convergence promise).

**Self-questions**
- *Why is `Load` used?* Probably copy-paste from the startup path. The correct CvRDT
  operation is `Merge` (`lwwmap.go:134`) or per-key `ApplyRegister`.
- *Does this run today?* Only if a `ClusterBus`/relay is wired; see H2. With the default
  binary it's dormant, but the code path is reachable via `/cluster/event` and is
  advertised as the sharding foundation.

**Fix:** Route every cluster mutation through `applyAndPersist` (persist + observe clock),
add epoch checks, and replace `r.doc.Load(ev.State)` with a merge
(`r.doc.Merge(snapshotMap)` or per-key `ApplyRegister`) so remote snapshots join, not clobber.

**Verify:** add a test that applies a cluster `op`/`snapshot`, restarts the room from the
store, and asserts the state survives and concurrent local writes are preserved.

---

### H2 — Cluster path is unwired, unauthenticated, and can stall a room

**Evidence**
- `cmd/syncwell/main.go` sets `Config{ClusterPeers, ClusterPath, ...}` but **never** sets
  `NodeID`, `Peers`, `Bus`, or `Registry`. So `h.bus == nil` and `h.registry == nil` always.
- `internal/server/server.go:132-135` — `ServeClusterEvent` returns
  `503 "cluster bus unavailable"` when `h.bus == nil`. A relayed event therefore can never
  be applied on a peer started from the binary → the documented `-cluster-peers` feature is
  end-to-end non-functional.
- `ServeClusterEvent` (`server.go:123-149`) has **no authentication** — anyone who can reach
  `/cluster/event` can inject arbitrary `op`/`presence`/`snapshot` events into any room.
- `HTTPClusterRelay.Publish` (`bus.go:141-172`) uses `http.DefaultClient` (**no timeout**)
  and is called **synchronously from the room goroutine** via `publishCluster`
  (`hub.go:394-406`, invoked at `hub.go:290/320/343`). A slow/hung peer stalls the entire
  room's `loop()`, blocking all joins/ops/presence for that room.

**Impact:** Advertised horizontal-scaling foundation does not work from the shipped binary;
if enabled, it adds an unauthenticated write endpoint and a single slow peer becomes a
room-wide denial of service.

**Self-questions**
- *Should we fix or remove?* Since it's labelled "stretch / future work", the honest move
  is either (a) wire it correctly with auth + async relay + timeouts, or (b) mark it
  clearly as not-yet-supported and remove the reachable `/cluster/event` from the router.
- *Where should relay happen?* Off the room goroutine (a bounded outbound queue + worker),
  with a client timeout, so the single-owner room never blocks.

**Fix:** register/wire the bus + registry (or remove the route), require the admin token on
`/cluster/event`, give the relay its own `http.Client{Timeout: ...}`, and publish
asynchronously. Then add a two-process integration test.

---

### H3 — Rooms are never evicted → unbounded memory growth

**Evidence**
- `internal/server/hub.go:513-527` `Hub.room()` creates a `Room` on first join and stores it
  in `h.rooms`; the only `delete(h.rooms...)` is `h.rooms = map[string]*Room{}` in
  `Shutdown` (`hub.go:544`).
- The `leave` path (`hub.go:263-266`) removes the *client* but never the *room*, so a Room
  goroutine (`go r.loop()`, `hub.go:168`) plus its entire CRDT lives forever.

**Impact:** Each distinct room id that ever receives a connection leaks a goroutine + CRDT
map for the process lifetime. An attacker (or a chatty integration) can create unbounded
rooms → memory/goroutine exhaustion (the exact DoS class the production audit flags as the
largest direct risk). `/stats` also grows without bound.

**Self-questions**
- *Why wasn't this caught?* No test leaves a room empty and asserts eviction; `MaxClients`
  limits clients per room, not the number of rooms.
- *How to evict safely?* Refcount by `len(r.clients)`; when it hits 0, start an idle timer
  (e.g. 30s grace for reconnects), then send a `shutdown`-like command that also
  `delete(h.rooms, id)` under `h.mu` (from the room goroutine's exit callback, not inline).

**Fix:** add an idle-room reaper with grace period + a global room-count cap
(configurable, with metrics). Persist/compact before eviction.

---

### H4 — Room creation performs disk I/O under the global hub mutex

**Evidence**
- `Hub.room()` takes `h.mu.Lock()` for the whole body (`hub.go:513-527`) and calls
  `newRoom(...)`.
- `newRoom()` synchronously calls `st.Load(id)` (`hub.go:157-167`), which `os.Open`s and
  scans the op-log (`store.go:76-130`). For a large/compacted room this can be many MB.

**Impact:** The first join to any room blocks *every* other room's lookup/shutdown (the hub
lock is global). A cold cache or a huge room log becomes a fleet-wide latency spike and can
stall `Stats()`/`ServeWS`.

**Fix:** create the room entry under the lock, then load the store **outside** the lock
(e.g. build the doc in a per-room constructor goroutine and gate the first `join` on
readiness), or per-room locks instead of one global mutex.

---

### H5 — GC low-water trusts client-reported clocks (data-loss vector)

**Evidence**
- `internal/server/hub.go:219-224` `noteClock` stores whatever counter the client sends.
- `hub.go:322-331` the `ack` handler calls `noteClock(c.client, c.in.Clock)` with a raw
  client value (no validation).
- `hub.go:226-243` `lowWater()` = min acked counter; `maintain()` (`hub.go:204-217`) then
  `CollectTombstones(lw)` with `TS.Counter < lw` (`lwwmap.go:185-194`).

**Impact:** The causal-stability guarantee ("no future write can fall below this") assumes
the client's advertised clock is truthful and at least its own future write counter. A
buggy or malicious client can `ack` a huge value; if it is the minimum across clients
(e.g. it is the newest joiner, resetting the min correctly for others), the server can
collect tombstones ahead of state that a legitimately offline replica still holds, so a
later reconnect can resurrect a deleted key — the resurrection/divergence the design
explicitly warns about (`docs/architecture/DESIGN.md` §4). Also a client can `ack` `math.MaxUint64`.

**Self-questions**
- *Can the server compute the bound itself?* Yes. Track, per connection, the highest
  counter the **server has actually delivered** to that client, and derive the low-water
  from delivered state, not from client claims. Optionally clamp `ack` to
  `min(clientClaim, serverIssuedMax)`.
- *Should offline clients block GC?* Documented trade-off; at minimum record bounded
  offline leases (already noted in `docs/protocol/ACCESS_CONTROL_AND_EPOCHS.md`).

**Fix:** clamp/validate ack counters (reject > server-known max), or compute low-water from
server-delivered versions. Add a test where a client lies about its clock and assert no
tombstone is collected below the true bound.

---

### H6 — Persistence filename can exceed the filesystem limit

**Evidence**
- `internal/store/store.go:66-68`: filename = `base64.RawURLEncoding(room) + ".log"`.
  base64 expands ~4/3, so a 256-byte room id → ~344 chars + `.log`.
- `internal/server/server.go:29` sets `maxRoomIDBytes = 256`, enforced at
  `server.go:199-202` (`room id too large` only above 256).
- Typical `NAME_MAX` is 255 bytes → `open(...)` returns `ENAMETOOLONG` for room ids
  ≳ 188 bytes.

**Impact:** Long-but-valid room ids pass admission but then every `Append` fails in
`applyAndPersist`, producing `"persistence failed"` error frames and an effectively
read-only room. Also affects `Compact` (`store.go:172-208`).

**Self-questions**
- *Lower the limit or hash the name?* Either. A SHA-256 (hex) filename is fixed-length and
  collision-resistant; base64 is injective but unbounded. Hashing is the safer default.
- *Do we still need the original id in the file?* `Record`/snapshot don't store the room id;
  if hash-only, keep a small room-id index (or accept hash filenames as opaque).

**Fix:** use `hex(sha256(room)) + ".log"` (fixed 68 chars) with migration, or cap
`maxRoomIDBytes` to ~180. Add a test with a 250-byte room id.

---

## MEDIUM

### M1 — `admin_token` accepted via URL query string
**Evidence:** `internal/server/server.go:170-172` falls back to `req.URL.Query().Get("admin_token")`.
**Impact:** tokens leak into access logs, proxies, and browser history. Prefer the
`Authorization: Bearer` header only (already supported at `server.go:167-169`).
**Fix:** drop the query fallback, or gate it behind an explicit dev flag.

### M2 — Client-chosen `TS.Node` is not bound to the connection
**Evidence:** `server.go:372-383` validates node length only; `hub.go` op/merge accept any
`TS.Node`. A client can claim any node id (including another client's) to influence the
`(Counter,Node)` total order.
**Impact:** ordering spoofing; undermines trust in "last writer wins" under adversarial
clients (ties are now deterministic via `lwwmap.go:36-50`, which limits the damage, but
impersonation remains).
**Fix:** bind the register node to the authenticated `Sub` (or the assigned server node id)
and reject mismatches; or namespace client node ids server-side.

### M3 — Go vs JS tie-break can diverge; no cross-language conformance tests
**Evidence:** Go `canonicalJSON` (`internal/crdt/lwwmap.go:46,66-84`) decodes with
`UseNumber` then `json.Marshal`; JS `stableJSON` (`clients/javascript/crdt.js:17-32,31`) uses
`JSON.stringify` with sorted keys. Edge cases diverge: lexical numbers (`1.0` vs `1`,
`1e2` vs `100`), and objects containing `undefined` vs omitted keys.
**Impact:** two replicas can order an equal-`(counter,node)` conflict differently →
non-convergence, precisely the class of bug `PRODUCTION_AUDIT` C1 was about (C1's
*tie-break existence* is fixed; *cross-language equivalence* is not verified).
**Fix:** share one canonicalization (e.g. JCS / RFC 8785) implemented identically, and add
a fixture-driven Go↔JS conformance test. `docs/IMPLEMENTATION_PHASES.md` Phase 1 still has
"cross-language protocol conformance tests" unchecked.

### M4 — `merge` failure path is noisy / ambiguous
**Evidence:** `hub.go:307-320`; on `applyAndPersist` failure it `continue`s the inner loop
and still sends a final snapshot, so a client can receive multiple `"persistence failed"`
error frames plus a snapshot.
**Impact:** client-side error handling can't distinguish "some keys failed" from "accepted";
makes recovery ambiguous.
**Fix:** collect failures, send one structured error, and skip the snapshot when durability
is required.

### M5 — `FileStore.Load` silently skips **all** malformed lines
**Evidence:** `store.go:115-125` `for sc.Scan()` `continue`s on any `json.Unmarshal` error,
not just the final torn line.
**Impact:** mid-file corruption (bit rot, partial compaction) is silently discarded, so the
document silently loses history. Also `.tmp` files from failed compact could linger
(`store.go:172-208`).
**Fix:** checksum/sequence-number each record; tolerate only a torn *final* line; otherwise
log+alert; clean temp files on failure.

### M6 — `membership.go` is dead code
**Evidence:** `internal/server/membership.go` defines `MemoryClusterRegistry`,
`ServeClusterJoin`, `ServeClusterMembers`, but `cmd/syncwell/main.go` registers neither route
and never sets `Config.Registry` (grep confirms no non-test usage).
**Impact:** misleading surface; doc/plan implies membership is available. Untested code rots.
**Fix:** either wire it (with auth) and test it, or delete it and correct the docs.

### M7 — Coverage gaps and `make all` omissions
**Evidence:** `Makefile` `all: vet test test-js e2e persist gc test-multi-client build`
omits `chaos`, `bench`, `browser-e2e` (CI runs chaos/bench separately; browser-E2E is
conditional on firefox/geckodriver). The production audit's "missing HTTP/WS contract
tests" is still true.
**Impact:** regressions in the omitted paths aren't gated locally; CI's browser check can
silently skip.
**Fix:** add `fmt-check`, chaos, and a non-browser protocol-contract suite to `make all`;
make CI fail (not skip) when the browser toolchain is absent, or provision it.

---

## LOW / DOCS / HYGIENE

### L1 — Documentation overstates status; size drift
**Evidence:** `README.md:3-4` still contains the `OWNER/REPO` badge placeholder;
`docs/product/PLAN.md` declares "engineering complete ✅ … 8/8" and "all green in `make all`", which is
false today (B1/B2/B3/B4). README claims "~8 MB" static binary; actual is ~10.5 MB.
`PRODUCTION_AUDIT_2026-07-30.md` explicitly warns about these claims (L1).
**Fix:** align README/PLAN with reality; wire the real `OWNER/REPO`; restate the measured
binary size; don't claim green CI until CI actually runs (B4) and passes (B1/B2).

### L2 — Nested git repositories inside the working tree
**Evidence:** `syncwell-engine/` and `syncwell-public/` each contain their own `.git`
(`git -C syncwell-engine rev-parse --is-inside-work-tree` → true; remotes
`github.com/Nil-Vaghani/syncwell-engine` / `syncwell-public`). `git status` lists them as
untracked directories; there is **no `.gitmodules`**. `git add -A` would create accidental
gitlinks/submodules and commit a nested tree the monorepo can't reproduce.
**Impact:** confusing, unreproducible history; risk of committing embedded repos or losing
work; duplicated/divergent copies of the same engine (maintenance drift).
**Fix:** decide the canonical source of truth. Either move the split repos out of the
monorepo working tree (e.g. sibling dirs), or register them as true submodules
(`git submodule add`) with pinned commits. Add them to `.gitignore` if they must stay as
local scratch. Never `git add -A` while they are present.

### L3 — Public split repo: supply-chain issues remain
**Evidence:** `syncwell-public/` has `go.mod` but **no `go.sum`**; `go.mod` requires
`gorilla/websocket` though the wrapper doesn't import it directly; `syncwell-public/Dockerfile`
still does `ARG GITHUB_TOKEN` + `git config --global url."https://${GITHUB_TOKEN}@github.com/".insteadOf ...`
— the token is written into a build layer and the builder's gitconfig (audit SC3/SH4).
The Dockerfile now uses exec-form `ENTRYPOINT ["/syncwell"] CMD [...]` (good, fixes SC2's
`busybox/sh`), but the secret handling is unfixed.
**Fix:** commit a tidy `go.sum`, drop unused direct deps, use BuildKit
`--mount=type=secret`/`--mount=type=ssh` (never `ARG`), and add a clean-build CI gate.

### L4 — Minor / nits
- `main.go` exposes `Config.NodeID/Peers/Bus/Registry/ClusterHTTP` that the binary never
  sets → untested API surface (ties into H2/M6).
- `version.String()` has no trailing newline; `-version` output depends on the shell
  (`version.go:13-15`) — harmless.
- `examples/demos/sequence.html` is a `BroadcastChannel`/localStorage prototype, not wired to the
  server-side sequence CRDT (`internal/sequence`, `clients/javascript/sequence.js`); label it clearly as a
  local prototype (it already says "local prototype") so it isn't mistaken for server sync.
- `projects_summary.md` is untracked but referenced in project context; decide to commit or
  remove it.

---

## Recommended remediation order

1. **B4** CI branch filter (1 line) — so future pushes are actually tested.
2. **B1** fix the SDK test + **B2** fix the E2E readiness probe — get `make all` green.
3. **B3** `make fmt` + add `fmt-check` gate.
4. **H3 / H4** room lifecycle + load-outside-lock (availability/leak).
5. **H6** storage filename length; **M5** corruption tolerance.
6. **H5 / M2** GC-bound + node binding (correctness/security under untrusted clients).
7. **H1 / H2 / M6** decide the cluster story: wire it correctly (auth, async, timeout,
   persist+merge) or remove the reachable surface and fix docs.
8. **M1 / M3 / M4** auth hygiene, cross-language canonicalization + conformance tests.
9. **L1 / L2 / L3** docs truth-up, remove nested repos from the tree, fix public repo
   supply chain.

## Definition of Done for this scan

- [x] `go build ./... && go vet ./... && gofmt -l .` (empty) all clean
- [x] `go test ./... -race` green
- [x] `cd clients/javascript && node --test` green (27 tests, incl. 9 conformance fixtures)
- [x] `make all` green (incl. `e2e`, `persist`, `gc`, `test-multi-client`, `chaos`)
- [x] CI runs on the actual default branch and reports green (`Nil-Vaghani/syncwell` @ `master`, run 39298596921; split `syncwell-public` run 39297691968 also green)
- [x] Each H-item has a regression test that fails before the fix
- [x] README/PLAN claims match measured reality (~10.5 MB binary, 2026-10-19 status)

## Resolution log (2026-10-19)

| ID | Status | What changed |
| --- | --- | --- |
| B1 | Fixed | SDK test now delivers a `snapshot` before `set()`; 27/27 JS tests pass. |
| B2 | Fixed | `tests/helpers.mjs` probes with `node:http` (`Connection: close`) instead of global `fetch`, avoiding the shared-undici CONNECTING hang; persist/offline/multi and all E2E harnesses use it. |
| B3 | Fixed | `gofmt -w .` + `fmt-check` target in `make all`. |
| B4 | Fixed | CI triggers on `main` and `master`. |
| H1 | Fixed | Cluster ops/snapshots now go through `applyAndPersist` (durable + clock-advancing); cluster snapshots merge per key instead of replacing. Regression test: `TestClusterOpIsPersistedOnReceiver`. |
| H2 | Fixed | Relay publishing is bounded-async with a 2s timeout; `/cluster/event` requires admin auth and the relay sends the admin bearer token. |
| H3 | Fixed | Live rooms bounded by `-max-rooms`; admission is checked before the WebSocket upgrade (`errRoomCapacity`); regression tests `TestHubMaxRoomsCapacity`, `TestRoomIdleEviction`. |
| H4 | Fixed | Store load moved into the room goroutine (`loadPersisted`), so replay never holds the hub mutex. |
| H5 | Fixed | Idle rooms are evicted after `-idle-room-ttl` (default 60s, negative disables); acknowledgements are clamped to what the server actually sent. |
| H6 | Fixed | Room filenames are SHA-256 hex (fixed length), with one-way migration from the base64 and legacy sanitised layouts. |
| M1 | Fixed | `admin_token` query fallback removed; only `Authorization: Bearer` is accepted (test asserts rejection). |
| M2 | Fixed | Each connection is pinned to one authoring node id; mismatched op/merge registers are rejected. |
| M3 | Fixed | Full RFC 8785 (JCS) canonicalisation in Go and the SDK: UTF-16 key sorting, ECMAScript number formatting (including `-0` and exponent thresholds), minimal string escaping (no HTML/U+2028/29 escaping), and code-point value comparison. 15 shared fixtures in `tests/conformance/` run from both suites. |
| M4 | Fixed | A failed `merge` emits one `persistence failed` error and no snapshot (no partial-durability false success). |
| M5 | Fixed | Only a torn final line is tolerated; mid-file corruption fails loudly. Tests: `TestLoadRejectsMidFileCorruption`, `TestLoadToleratesTornLine`. |
| M6 | Fixed | Dead `membership.go`/`ClusterRegistry` removed; `Config.Registry` no longer exists. |
| L1 | Fixed | README badge placeholder clarified, binary size updated to ~10.5 MB, PLAN status restated and linked to this audit. |
| L2 | Fixed | `syncwell-engine/` and `syncwell-public/` removed from the working tree after verifying they were clean/pushed. |
| L3 | Fixed | The affected nested tree is out of this repo. The standalone `Nil-Vaghani/syncwell-public` repo was also fixed: `go.sum` committed, `gorilla/websocket` demoted to indirect, Dockerfile uses a BuildKit `github_token` secret (no `ARG`/layer leak), CI uses `ENGINE_TOKEN`, and its pipeline is green (run 39297691968). Replace the broad `ENGINE_TOKEN` secret with a fine-scoped read-only PAT when convenient. |

_Also fixed while verifying CI:_ `tests/e2e/browser_kanban_e2e.mjs` hard-coded an
owner-specific absolute path to `examples/demos/kanban.html`; it now resolves the demo
relative to the test file, so browser E2E runs on any checkout.
