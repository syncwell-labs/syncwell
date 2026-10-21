# syncwell-client

Zero-dependency browser/Node client for the [Syncwell](../README.md) real-time
collaboration engine. Holds a local CRDT replica so writes apply instantly and
work offline, converging with the server on reconnect.

```bash
npm install syncwell-client
```

```js
import { Syncwell } from "syncwell-client";

const sw = new Syncwell("ws://localhost:8080/ws", "my-room", {
  token: "optional-room-token",
  clientId: "stable-user-or-tab-id",
  persist: true,
});
sw.on("change", (live) => render(live)); // live = { key: value, ... }
sw.connect();

sw.set("title", "Hello, multiplayer"); // applies locally + syncs
sw.delete("title");
sw.presence({ cursor: { x: 12, y: 40 } }); // ephemeral, not persisted
```

## API

| method | description |
| --- | --- |
| `new Syncwell(url, room, opts?)` | `opts.clientId`, `opts.token`, `opts.persist`, `opts.storage`, `opts.storageKey`, `opts.WebSocket` (for Node), `opts.delta`, `opts.historyLimit` |
| `ready` | resolves after the initial persistence load |
| `connect()` / `close()` | open / close the socket (auto-reconnect on drop) |
| `set(key, value)` / `delete(key)` | optimistic local write, then sync |
| `get(key)` / `live()` | read from the local replica |
| `batch(fn)` | group writes into one change event and one undo step |
| `undo()` / `redo()` / `canUndo()` / `canRedo()` | per-client history; remote winners are never clobbered |
| `presence(data)` | ephemeral broadcast (cursors, who's-online) |
| `on(event, cb)` / `off(event, cb?)` | `change`, `presence`, `leave`, `open`, `close`, `error`, `history` |

### Collaborative text

`syncwell-client/text` adds a character-level sequence CRDT on top of the same
room and transport:

```js
import { SequenceText } from "syncwell-client/text";

const text = new SequenceText(sw, { prefix: "body:" });
text.insertAt(0, "Hello");
text.replaceRange(5, 0, ", world");
text.markRange(0, 5, { bold: true });
text.on("change", () => console.log(text.text(), text.visibleIds()));
```

Offsets are in Unicode code points. Inserts interleave by rank rather than
colliding on a numeric index, so concurrent typing at the same position merges
deterministically. Insertions and deletions are tombstones, so undo/redo and
offline edits remain causally safe.

### Typed awareness

```js
import { Awareness } from "syncwell-client/awareness";

const awareness = new Awareness(sw, {
  user: { id: "u1", name: "Ada", color: "#2f6bff" },
  heartbeat: 10_000,
  timeout: 30_000,
});
awareness.setSelection(12, 18);
awareness.setCursor({ index: 18 });
awareness.on("update", (peers) => renderCursors(peers.filter((p) => !p.self)));
```

Awareness is ephemeral (`presence` frames), never persisted, and heartbeats so
stale peers age out even if a socket disappears without a close frame.

The main module also exports `protocolVersion` and `SyncwellError`. The `crdt`
submodule (`syncwell-client/crdt`) exports `LWWMap`, `Clock`, and `tsAfter` if
you want the bare CRDT.

### TypeScript

Declarations ship with the package; type the document once and every method is
checked:

```ts
import { Syncwell } from "syncwell-client";

const sw = new Syncwell<{ title: string; done: boolean }>(
  "wss://example/ws",
  "room-1",
);
sw.on("change", (live) => live.title.toUpperCase());
sw.set("done", true); // ok
// sw.set("done", "yes"); // type error
```

### Builds

| Entry | Format | Use |
| --- | --- | --- |
| `syncwell-client` | ESM + CJS (conditional exports) | bundlers, Node 18+ |
| `syncwell-client/browser` | minified IIFE + source map | `<script src="dist/syncwell.min.js">` (global `SyncwellSDK`) |
| `syncwell-client/crdt`, `/sequence` | ESM + CJS | bare CRDT primitives |

`npm run build` writes `dist/` (built in CI and on `prepublishOnly`; not
committed). The shipped package has no runtime dependencies — esbuild is a
devDependency only.

## Offline persistence

Offline edits always stay in memory. Set `persist: true` to also save the local
CRDT snapshot, Lamport clock, dirty keys, and undo/redo stacks in
`localStorage`, or pass a Storage-compatible object with `opts.storage`.
Adapters are exported for every runtime:

```js
import { MemoryStorage, LocalStorageAdapter, indexedDBStorage } from "syncwell-client/storage";
import { fileStorage } from "syncwell-client/storage-node";

const browser = new Syncwell(url, room, { storage: indexedDBStorage("my-app") });
const node = new Syncwell(url, room, { storage: fileStorage(".syncwell/cache.json") });
```

Use a stable `clientId` when enabling persistence so a reload keeps the same
clock identity. Async adapters (IndexedDB, file) are awaited through `sw.ready`.

## Delta sync

On reconnect the client sends a `hello` with its per-key state vector and only
the dirty registers it wrote offline. The server replies with a `delta` of
registers the client is missing instead of a full snapshot. This is enabled by
default; set `delta: false` to force the snapshot/merge exchange. The wire
format is documented in [`docs/PROTOCOL.md`](../docs/PROTOCOL.md).

`error` handlers receive `SyncwellError` objects with a `code`, including
`NO_WEBSOCKET`, `BAD_FRAME`, `SERVER_ERROR`, `STORAGE_READ`, and
`STORAGE_WRITE`.

## Framework examples

Copy-paste integrations live in [`examples/`](../examples): a React hook, a Vue
composable, a Svelte store, a vanilla page, and a Node mirror/bot. The Python
client in [`python/`](../python) proves the protocol is language-agnostic.

## Tests

```bash
node --test        # pure CRDT unit tests
npm run typecheck  # compile the .d.ts contract
npm run build      # ESM + CJS + minified browser bundle with source maps
```

MIT licensed.
