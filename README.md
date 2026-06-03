# Syncwell

**Add multiplayer — live cursors, presence, and conflict-free shared state — to any app with a few lines of code. Self-hostable, single binary, language-agnostic.**

Syncwell is an open-source, self-hostable real-time collaboration engine — an alternative to closed SaaS like Liveblocks and PartyKit. Drop in the client SDK, point it at a Syncwell server, and your users get Google-Docs-style live sync. The server compiles to a single dependency-free binary you can run anywhere.

```
┌──────────┐   ws    ┌─────────────────────┐   ws   ┌──────────┐
│ Client A │◀───────▶│  Syncwell server     │◀──────▶│ Client B │
│ (browser)│         │  • CRDT per room     │        │ (browser)│
└──────────┘         │  • presence relay    │        └──────────┘
                     │  • single 8 MB binary│
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
| binary size | single **~8 MB** static binary, zero runtime deps |

**Proven correct** (all green in `make all`):
- ⚡ CRDT laws — commutativity, associativity, idempotence (Go + JS suites)
- 💥 **Convergence under chaos** — 5 replicas × 3000 ops with partitions,
  reordering, and duplicate delivery converge byte-identical (`make chaos`)
- 🔌 **Offline → reconnect** convergence with no lost edits (`make e2e`)
- 💾 **Durability** across `kill -9` + restart (`make persist`)
- 🗑️ **Tombstone GC** — 40 deletes collapse to ~1 via a causal-stability
  low-water mark (`make gc`)

_Still ahead (see [docs/ROADMAP.md](docs/ROADMAP.md)): hosted demo URL, npm SDK,
GitHub stars / Docker pulls / self-hosting users._

## Quickstart

```bash
# Run the server (serves the demo at / and websockets at /ws)
go run ./cmd/syncwell
# open http://localhost:8080 in two browser windows and type
```

Or build the single binary:

```bash
make build      # -> ./bin/syncwell
./bin/syncwell  # listens on :8080
```

Use it from the browser:

```js
import { Syncwell } from "./sdk/syncwell.js";

const sw = new Syncwell("ws://localhost:8080/ws", "my-room");
sw.on("snapshot", ({ state }) => render(state));
sw.on("op", ({ key, register }) => applyChange(key, register));
sw.connect();

sw.set("title", "Hello, multiplayer");   // syncs to everyone in "my-room"
sw.presence({ cursor: { x: 12, y: 40 } }); // ephemeral, not persisted
```

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
