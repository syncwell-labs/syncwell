# syncwell-client

Zero-dependency browser/Node client for the [Syncwell](../README.md) real-time
collaboration engine. Holds a local CRDT replica so writes apply instantly and
work offline, converging with the server on reconnect.

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
| `new Syncwell(url, room, opts?)` | `opts.clientId`, `opts.token`, `opts.persist`, `opts.storage`, `opts.storageKey`, `opts.WebSocket` (for Node) |
| `connect()` / `close()` | open / close the socket (auto-reconnect on drop) |
| `set(key, value)` / `delete(key)` | optimistic local write, then sync |
| `get(key)` / `live()` | read from the local replica |
| `presence(data)` | ephemeral broadcast (cursors, who's-online) |
| `on(event, cb)` | `change`, `presence`, `leave`, `open`, `close`, `error` |

The main module also exports `protocolVersion` and `SyncwellError`. The `crdt`
submodule (`syncwell-client/crdt`) exports `LWWMap`, `Clock`, and `tsAfter` if
you want the bare CRDT.

## Offline persistence

Offline edits always stay in memory. Set `persist: true` to also save the local
CRDT snapshot and Lamport clock in `localStorage`, or pass a Storage-compatible
object with `opts.storage`.

Use a stable `clientId` when enabling persistence so a reload keeps the same
clock identity:

```js
const sw = new Syncwell("wss://sync.example/ws", "my-room", {
  clientId: userOrTabId,
  persist: true,
});
```

`error` handlers receive `SyncwellError` objects with a `code`, including
`NO_WEBSOCKET`, `BAD_FRAME`, `SERVER_ERROR`, `STORAGE_READ`, and
`STORAGE_WRITE`.

## Tests

```bash
node --test   # pure CRDT unit tests
```

MIT licensed.
