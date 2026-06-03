# syncwell-client

Zero-dependency browser/Node client for the [Syncwell](../README.md) real-time
collaboration engine. Holds a local CRDT replica so writes apply instantly and
work offline, converging with the server on reconnect.

```js
import { Syncwell } from "syncwell-client";

const sw = new Syncwell("ws://localhost:8080/ws", "my-room");
sw.on("change", (live) => render(live)); // live = { key: value, ... }
sw.connect();

sw.set("title", "Hello, multiplayer"); // applies locally + syncs
sw.delete("title");
sw.presence({ cursor: { x: 12, y: 40 } }); // ephemeral, not persisted
```

## API

| method | description |
| --- | --- |
| `new Syncwell(url, room, opts?)` | `opts.clientId`, `opts.WebSocket` (for Node) |
| `connect()` / `close()` | open / close the socket (auto-reconnect on drop) |
| `set(key, value)` / `delete(key)` | optimistic local write, then sync |
| `get(key)` / `live()` | read from the local replica |
| `presence(data)` | ephemeral broadcast (cursors, who's-online) |
| `on(event, cb)` | `change`, `presence`, `leave`, `open`, `close` |

The `crdt` submodule (`syncwell-client/crdt`) exports `LWWMap`, `Clock`, and
`tsAfter` if you want the bare CRDT.

## Tests

```bash
node --test   # pure CRDT unit tests
```

MIT licensed.
