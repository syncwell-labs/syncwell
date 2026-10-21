# Getting started

Syncwell is a server plus a client SDK. The server owns rooms, persistence, auth,
and fan-out; the SDK keeps a local CRDT replica so the UI is instant and keeps
working offline.

## 1. Run the server

For local development, bind loopback so credentials are not required:

```bash
go run ./cmd/syncwell -addr 127.0.0.1:8080
```

Open any demo:

| URL | What it shows |
| --- | --- |
| `http://localhost:8080/` | Structured state and presence |
| `http://localhost:8080/kanban.html` | Multiplayer kanban |
| `http://localhost:8080/sequence.html` | Collaborative text, cursors, undo/redo |

A public bind (`:8080`, Docker, or a platform `PORT`) generates a room secret and
admin token on first start and logs them once. For stable credentials:

```bash
export SYNCWELL_SECRET=$(openssl rand -hex 32)
export SYNCWELL_ADMIN_TOKEN=$(openssl rand -hex 32)
go build -o bin/syncwell ./cmd/syncwell
./bin/syncwell -addr :8080
```

Mint a room token when `-secret` is set:

```bash
./bin/syncwell -secret "$SYNCWELL_SECRET" -mint my-room
# open http://localhost:8080/?token=<token>
```

## 2. Install the client

The server serves a ready-to-import copy of the SDK at `/sdk/`, so the fastest
path needs no install:

```js
import { Syncwell } from "/sdk/syncwell.js";
```

For a bundler or Node setup, copy the `sdk/` directory into your project (or
install `npm install syncwell-client` once the package is published — the API
is identical).

## 3. Write and read

```js
import { Syncwell } from "/sdk/syncwell.js";

const sw = new Syncwell("ws://localhost:8080/ws", "my-room", {
  clientId: "user-42",
  persist: true, // localStorage (IndexedDB/file adapters also available)
});

sw.on("change", (live) => render(live));
sw.on("open", () => console.log("connected"));
sw.on("close", () => console.log("offline; edits are queued"));

sw.ready.then(() => {
  render(sw.live()); // hydrate from local persistence
  sw.connect();
});

sw.set("title", "Hello, multiplayer");
sw.delete("obsolete-key");
```

Writes apply locally first and then sync. `change` fires for local and remote
edits, so the UI always reads from `sw.live()`.

## 4. Next steps

- [Core concepts](/guide/concepts) — rooms, registers, clocks, tombstones.
- [JavaScript SDK](/guide/sdk) — full API and TypeScript generics.
- [Text and awareness](/guide/text-and-awareness) — `SequenceText` and cursors.
- [Persistence](/guide/persistence) — IndexedDB and Node file adapters.
- [Self-hosting](/self-hosting/quickstart) — Docker, TLS, backups, metrics.
