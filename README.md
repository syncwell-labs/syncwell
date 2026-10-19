# Syncwell

[![CI](https://github.com/Nil-Vaghani/syncwell/actions/workflows/ci.yml/badge.svg)](https://github.com/Nil-Vaghani/syncwell/actions/workflows/ci.yml)
&nbsp;![Go](https://img.shields.io/badge/Go-1.23-00ADD8) ![License](https://img.shields.io/badge/license-MIT-blue)

**Add multiplayer — live cursors, presence, and conflict-free shared state — to any app with a few lines of code. Self-hostable, single binary, language-agnostic.**

Syncwell is an open-source, self-hostable real-time collaboration engine — an alternative to closed SaaS like Liveblocks and PartyKit. Drop in the client SDK, point it at a Syncwell server, and your users get Google-Docs-style live sync. The server compiles to a single dependency-free binary you can run anywhere.

```
┌──────────┐   ws    ┌─────────────────────┐   ws   ┌──────────┐
│ Client A │◀───────▶│  Syncwell server     │◀──────▶│ Client B │
│ (browser)│         │  • CRDT per room     │        │ (browser)│
└──────────┘         │  • presence relay    │        └──────────┘
                     │  • single ~10.5 MB binary│
                     └─────────────────────┘
```

## Why it exists

Real-time collaboration is something every product wants and few build well: concurrent edits, offline clients, and reconnection are genuinely hard. The hosted options are proprietary and per-seat priced. Syncwell gives you the same capability as an MIT-licensed binary you own.

## Status & numbers

Offline-capable, durable, and bounded-memory. Clients edit locally (even while
disconnected) and converge on reconnect; the server persists to disk, survives
`kill -9`, and garbage-collects tombstones so a long-lived room stays small.

**Measured** (50 concurrent clients, one room, single box — reproduce with `make bench`):

| metric | value |
| --- | --- |
| sync latency p50 / p95 / p99 | **1.3 / 2.5 / 3.6 ms** |
| delivery throughput | **~9,000 msg/s** |
| server memory (RSS) | **11.5 MB** |
| binary size | single **~10.5 MB** static binary, zero runtime deps |

**Proven correct** (all green in `make all`):
- ⚡ CRDT laws — commutativity, associativity, idempotence (Go + JS suites)
- 💥 **Convergence under chaos** — 5 replicas × 3000 ops with partitions,
  reordering, and duplicate delivery converge byte-identical (`make chaos`)
- 🔌 **Offline → reconnect** convergence with no lost edits (`make e2e`)
- 💾 **Durability** across `kill -9` + restart (`make persist`)
- 🗑️ **Tombstone GC** — 40 deletes collapse to ~1 via a causal-stability
  low-water mark (`make gc`)

_Still ahead (see [docs/ROADMAP.md](docs/ROADMAP.md)): hosted demo URL, npm SDK,
GitHub stars / Docker pulls / self-hosting users. For the unfiltered list of what
is **not** production-ready, read
[docs/READINESS_AUDIT_2026-10-19.md](docs/READINESS_AUDIT_2026-10-19.md)._

## Quickstart

```bash
# Run the server (serves the demos at / and websockets at /ws)
go run ./cmd/syncwell
# open http://localhost:8080            → simple text demo (two windows)
# open http://localhost:8080/kanban.html → collaborative kanban (drag cards live)
```

### Production knobs (all optional)

```bash
syncwell \
  -data ./data \                 # persist to disk (survives restart)
  -secret "$SECRET" \            # require room tokens (HMAC, expiring)
  -admin-token "$ADMIN_TOKEN" \   # protect /stats
  -origins https://app.example \ # Origin allow-list
  -max-clients 200 \             # per-room cap
  -max-messages-per-second 120   # per-client rate limit

# issue a 24h token for a room:
syncwell -secret "$SECRET" -mint my-room
# clients connect with:
# new Syncwell("wss://host/ws", "my-room", { token })
```

Or build the single binary:

```bash
make build      # -> ./bin/syncwell
./bin/syncwell  # listens on :8080
./bin/syncwell -version
```

Or run it in Docker (multi-stage build → distroless, ~10 MB image):

```bash
make docker VERSION=0.2.0
docker run -p 8080:8080 syncwell
```

Use it from the browser:

```js
import { Syncwell } from "./sdk/syncwell.js";

const sw = new Syncwell("ws://localhost:8080/ws", "my-room", {
  token,              // optional when the server runs with -secret
  clientId: userId,   // keep stable if you enable persistence
  persist: true,      // stores offline edits in localStorage
});
sw.on("change", (live) => render(live));
sw.on("error", (err) => console.error(err.code, err.message));
sw.connect();

sw.set("title", "Hello, multiplayer");   // syncs to everyone in "my-room"
sw.presence({ cursor: { x: 12, y: 40 } }); // ephemeral, not persisted
```

For hosted deployments, put Syncwell behind TLS, configure `-origins`, require
room tokens with `-secret`, protect `/stats` with `-admin-token`, use
`/healthz` and `/readyz` for probes, and back up the `-data` directory. See
[docs/OPERATIONS.md](docs/OPERATIONS.md).

## How it works

A **room** is one collaborative document, backed by a [CRDT](docs/../DESIGN.md) so concurrent edits merge without conflicts. Each room is owned by a single goroutine that is the only code allowed to touch the document — so there are **no locks**, and concurrency is structured as message passing. Clients get a full **snapshot** on join, then a stream of **ops**; ephemeral **presence** (cursors, who's-online) is relayed but never stored.

Read the design rationale and trade-offs in **[DESIGN.md](DESIGN.md)** — including why CRDT over Operational Transform, and the tombstone-growth problem with its planned fix.

## Layout

```
cmd/syncwell/      single-binary entrypoint (HTTP + WebSocket)
internal/crdt/     the CRDT core + property test suite  ← the heart
internal/server/   room hub, websocket pumps, presence relay
sdk/syncwell.js    zero-dependency browser client
demo/              runnable multiplayer demo (open in two windows)
docs/              PROTOCOL.md, ROADMAP.md
```

## Roadmap

Phased so each step is independently shippable — see **[docs/ROADMAP.md](docs/ROADMAP.md)**.
Phase 1 ✅ shared state + presence · Phase 2 offline/client-side CRDT + persistence · Phase 3 SDK polish, auth, Docker release · Stretch: horizontal scaling.

## License

MIT — see [LICENSE](LICENSE).
