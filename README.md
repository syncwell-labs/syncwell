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

> Phase 1 (current): conflict-free shared key/value documents over WebSocket, live presence, auto-reconnect. CRDT correctness is proven by a property test suite (commutativity, associativity, idempotence, and convergence under concurrent edits — all green).

- **Core:** state-based LWW-Map CRDT, zero third-party deps
- **Server:** single ~8 MB binary, lock-free per-room concurrency model
- _Replace these with your real measurements as you build — see [docs/ROADMAP.md](docs/ROADMAP.md):_
  - ⚡ sync latency p99 under N concurrent clients
  - 💥 verified convergence after simulated network partition
  - 📈 GitHub stars · Docker pulls · self-hosting users

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
