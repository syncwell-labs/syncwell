# Syncwell

[![CI](https://github.com/Nil-Vaghani/syncwell/actions/workflows/ci.yml/badge.svg)](https://github.com/Nil-Vaghani/syncwell/actions/workflows/ci.yml)
&nbsp;![Go](https://img.shields.io/badge/Go-1.23-00ADD8) ![License](https://img.shields.io/badge/license-MIT-blue)

**Add multiplayer — live cursors, presence, and conflict-free shared state — to any app with a few lines of code. Self-hostable, single binary, language-agnostic.**

Syncwell is an open-source, self-hostable real-time collaboration engine — an alternative to closed SaaS like Liveblocks and PartyKit. Drop in the client SDK, point it at a Syncwell server, and your users get Google-Docs-style live sync. The server compiles to a single static binary with no runtime dependencies — no database, cache, or message broker to operate.

```
┌──────────┐   ws    ┌─────────────────────┐   ws   ┌──────────┐
│ Client A │◀───────▶│  Syncwell server     │◀──────▶│ Client B │
│ (browser)│         │  • CRDT per room     │        │ (browser)│
└──────────┘         │  • presence relay    │        └──────────┘
                     │  • single ~20.0 MB binary│
                     └─────────────────────┘
```

## Why it exists

Real-time collaboration is something every product wants and few build well: concurrent edits, offline clients, and reconnection are genuinely hard. The hosted options are proprietary and per-seat priced. Syncwell gives you the same capability as an MIT-licensed binary you own.

## Status & numbers

Offline-capable, durable, and bounded-memory. Clients edit locally (even while
disconnected) and converge on reconnect; the server persists to disk, survives
`kill -9` (and power loss with `-fsync every`), and garbage-collects tombstones so a long-lived room stays small.

**Indicative, not a service-level benchmark** (50 concurrent clients, one room, single box — reproduce with `make bench`):

| metric | value |
| --- | --- |
| sync latency p50 / p95 / p99 | **1.3 / 2.5 / 3.6 ms** |
| delivery throughput | **~9,000 msg/s** |
| server memory (RSS) | **11.5 MB** |
| binary size | single **~20.0 MB** static binary, zero runtime deps |

**Proven correct** (all green in `make all`):
- ⚡ CRDT laws — commutativity, associativity, idempotence (Go + JS suites)
- 💥 **Convergence under chaos** — 5 replicas × 3000 ops with partitions,
  reordering, and duplicate delivery converge byte-identical (`make chaos`)
- 🔌 **Offline → reconnect** convergence with no lost edits (`make e2e`)
- 💾 **Durability** across `kill -9` + restart (`make persist`), plus checksummed backup/restore (`make restore-drill`)
- 🗑️ **Tombstone GC** — 40 deletes collapse to ~1 via a causal-stability
  low-water mark (`make gc`)
- 🌐 **Two-node convergence** through the HTTP relay, with anti-entropy healing
  dropped events (Go integration test in `internal/server`)
- ✍️ **Collaborative text** — character-level sequence CRDT, rich-text marks,
  undo/redo, and live cursors (`sdk/text.js`, `sdk/awareness.js`, `make test-js`)
- 🐍 **Language-agnostic** — a dependency-free Python client converges with the
  browser SDK against the real server (`make test-python`)

_Phase 7 code is in place: the VitePress site under [`website/`](website/) builds
in CI, protocol v1 is frozen, and the launch/demo runbook is written. Still
owner-only (see [docs/LAUNCH.md](docs/LAUNCH.md) and
[docs/MILESTONES.md](docs/MILESTONES.md)): enable GitHub Pages, publish the
release tags/npm package/GHCR image, deploy the tag-pinned demo with a status
page, refresh the live metrics, and publish the launch post. For the unfiltered
list of what is **not** production-ready, read
[docs/READINESS_AUDIT_2026-10-19.md](docs/READINESS_AUDIT_2026-10-19.md); for the
plan to close every gap, read
[docs/COMPLETION_PLAN_2026-10-20.md](docs/COMPLETION_PLAN_2026-10-20.md)._

## Quickstart

```bash
# Local dev: bind loopback so no credentials are needed.
go run ./cmd/syncwell -addr 127.0.0.1:8080
# open http://localhost:8080             → simple state demo (two windows)
# open http://localhost:8080/kanban.html → collaborative kanban (drag cards live)
# open http://localhost:8080/sequence.html → collaborative text, cursors, undo/redo
```

A **public bind** (the default `:8080`, Docker, or a platform's `PORT`) with no
`-secret`/`-admin-token` now generates both once and prints them to the log; set
the flags/env yourself for stable credentials:

```bash
export SYNCWELL_SECRET=$(openssl rand -hex 32)
export SYNCWELL_ADMIN_TOKEN=$(openssl rand -hex 32)
./bin/syncwell -addr :8080
# mint a token for a room (demo pages accept ?token=...):
./bin/syncwell -secret "$SYNCWELL_SECRET" -mint demo
# open http://localhost:8080/?token=<token>
```

### Production knobs (all optional)

```bash
syncwell \
  -data ./data \                 # persist to disk (survives restart)
  -secret "$SECRET" \            # require room tokens (HMAC, expiring)
  -admin-token "$ADMIN_TOKEN" \   # protect /stats,/metrics,/cluster
  -admin-allowlist 10.0.0.0/8 \  # optional CIDR allow-list for admin
  -secret-previous "$OLD" \      # accept old tokens during rotation
  -oidc-issuer https://idp.example \ # accept IdP JWTs (room + admin caps)
  -otel-endpoint http://collector:4318 \ # optional OTLP/HTTP traces
  -origins https://app.example \ # Origin allow-list
  -max-clients 200 \             # per-room cap
  -max-messages-per-second 120 \ # per-client rate limit
  -fsync batch \                 # os | batch (default) | every (power-loss safe)
  -max-log-bytes 67108864 \      # compact a room log at 64 MiB (0 = unlimited)
  -node-id node-a \              # unique id (required when clustering)
  -cluster-peers http://b:8080   # full-mesh replication peers

# offline backup / verify / restore (checksummed manifest):
syncwell -data ./data -backup ./backup
syncwell -verify-backup ./backup
syncwell -restore ./backup -data ./data

# issue a 24h token for a room:
syncwell -secret "$SECRET" -mint my-room
# clients connect with:
# new Syncwell("wss://host/ws", "my-room", { token })
```

Or build the single binary:

```bash
make build      # -> ./bin/syncwell
./bin/syncwell -addr 127.0.0.1:8080   # local dev (no credentials)
./bin/syncwell -version
```

Or run it in Docker (multi-stage build → distroless, non-root, ~10 MB image):

```bash
make docker VERSION=0.2.0
# local demo only (explicitly opts out of generated credentials):
docker run --rm -p 127.0.0.1:8080:8080 -e SYNCWELL_INSECURE=1 syncwell
# production-style run with explicit, reusable credentials:
SECRET=$(openssl rand -hex 32); ADMIN=$(openssl rand -hex 32)
docker run --rm -p 8080:8080 -e SYNCWELL_SECRET=$SECRET -e SYNCWELL_ADMIN_TOKEN=$ADMIN syncwell
# mint a room token for the demo page:
docker run --rm -e SYNCWELL_SECRET=$SECRET syncwell -mint demo
# open http://localhost:8080/?token=<token>
# or the full self-host stack (secrets + persistent volume + volume chown):
cp .env.example .env   # set SYNCWELL_SECRET and SYNCWELL_ADMIN_TOKEN
docker compose up -d
```

Prebuilt archives for Linux, macOS, and Windows (amd64/arm64) are attached to
each [release](https://github.com/Nil-Vaghani/syncwell/releases/latest), with
`checksums.txt` and SPDX SBOMs. Platform templates live in
[`fly.toml`](fly.toml), [`render.yaml`](render.yaml),
[`docker-compose.yml`](docker-compose.yml), and [`deploy/`](deploy/).

### TLS without a proxy

```bash
syncwell -tls-cert /etc/letsencrypt/live/HOST/fullchain.pem \
         -tls-key  /etc/letsencrypt/live/HOST/privkey.pem
```

Or set `SYNCWELL_TLS_CERT`/`SYNCWELL_TLS_KEY`. TLS 1.2+ and HTTP/2 are enabled;
certificate issuance/renewal stays with your proxy or ACME client.

Use it from the SDK copy served by the engine (works today, no install):

```js
import { Syncwell } from "/sdk/syncwell.js";

const sw = new Syncwell("ws://localhost:8080/ws", "my-room", {
  token,              // optional when the server runs with -secret
  clientId: userId,   // keep stable if you enable persistence
  persist: true,      // localStorage; or pass IndexedDB/file adapters
  delta: true,        // reconnect with a state-vector diff (default)
});
sw.on("change", (live) => render(live));
sw.on("error", (err) => console.error(err.code, err.message));
sw.connect();

sw.set("title", "Hello, multiplayer");   // syncs to everyone in "my-room"
sw.undo();                               // grouped, won't clobber newer remote writes
sw.presence({ cursor: { x: 12, y: 40 } }); // ephemeral, not persisted
```

Need character-level text or typed cursors? Use the helpers:

```js
import { SequenceText } from "/sdk/text.js";
import { Awareness } from "/sdk/awareness.js";

const text = new SequenceText(sw);        // collaborative character sequence
const peers = new Awareness(sw, { user: { id: userId, name: "Ada" } });
text.insertAt(0, "Hello");
peers.setSelection(5);
text.on("change", () => renderText(text.text()));
```

For a bundler/Node setup you can also install the npm package once it is
published (`npm install syncwell-client`, same API, TypeScript types included;
until then copy the `sdk/` directory or import from your own `/sdk/` mount).

For hosted deployments, put Syncwell behind TLS, configure `-origins`, require
room tokens with `-secret`, protect `/stats` with `-admin-token`, use
`/healthz` and `/readyz` for probes, and back up the `-data` directory. See
[docs/OPERATIONS.md](docs/OPERATIONS.md) and the
[threat model](docs/THREAT_MODEL.md) (including residual risks). Inspect and
repair live rooms at `/admin` and subscribe to signed room milestones with
`-webhook-urls`; see [docs/ADMIN_AND_WEBHOOKS.md](docs/ADMIN_AND_WEBHOOKS.md).
Metrics use Prometheus histograms; a Grafana dashboard, alert rules, SLOs, and
load results ship in [docs/ALERTS.md](docs/ALERTS.md), [docs/SLO.md](docs/SLO.md),
and [docs/LOAD_TEST.md](docs/LOAD_TEST.md).

## How it works

A **room** is one collaborative document, backed by a [CRDT](DESIGN.md) so concurrent edits merge without conflicts. Each room is owned by a single goroutine that is the only code allowed to touch the document — so there are **no locks**, and concurrency is structured as message passing. Clients get a full **snapshot** on first join; returning clients can opt into a state-vector **delta** so only missed registers cross the wire, then a stream of **ops**. Ephemeral **presence** (cursors, who's-online) is relayed but never stored.

Read the design rationale and trade-offs in **[DESIGN.md](DESIGN.md)** — including why CRDT over Operational Transform, and the tombstone-growth problem with its planned fix. Wire changes follow the frozen **[protocol compatibility policy](docs/PROTOCOL_COMPATIBILITY.md)**; contribution and release process are in **[CONTRIBUTING.md](CONTRIBUTING.md)** and **[docs/GOVERNANCE.md](docs/GOVERNANCE.md)**.

## Layout

```
cmd/syncwell/      single-binary entrypoint (HTTP + WebSocket)
internal/crdt/     the CRDT core + property test suite  ← the heart
internal/server/   room hub, websocket pumps, presence relay, admin, webhooks
sdk/               typed browser/Node client: map, text, awareness, storage
python/            dependency-free Python client + real-server E2E test
examples/          React, Vue, Svelte, vanilla, and Node integrations
demo/              multiplayer state, kanban, and collaborative text demos
website/           VitePress docs site (quickstart, SDK, self-hosting, reference)
docs/              protocol, compatibility, operations, admin/webhooks, launch
```

## Roadmap

Phased so each step is independently shippable — see **[docs/ROADMAP.md](docs/ROADMAP.md)**.
Phases 1–6 ✅ shared state + presence · offline CRDT + persistence · typed SDK,
auth, Docker release · security & observability · product features (text,
awareness, undo, delta sync, admin, webhooks, Python client). Phase 7 local work
is complete — docs site, frozen v1 policy, governance, and launch runbook — with
hosting and publishing owner-only. See [docs/MILESTONES.md](docs/MILESTONES.md).

## License

MIT — see [LICENSE](LICENSE).
