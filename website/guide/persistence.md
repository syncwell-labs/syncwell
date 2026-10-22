# Persistence and offline

Local edits always stay in memory. Persistence adds a durable local replica so a
reload keeps offline edits, the Lamport clock, dirty keys, and undo/redo stacks.

Use a stable `clientId`; otherwise a reload looks like a new replica. Snippets
import `syncwell-client`; until the package is published, copy `clients/javascript/` or import
from your server's `/sdk/` mount.

## Browser: localStorage

```js
const sw = new Syncwell(url, "room", {
  clientId: userId,
  persist: true,
});
```

## Browser: IndexedDB

```js
import { indexedDBStorage } from "syncwell-client/storage";

const sw = new Syncwell(url, "room", {
  clientId: userId,
  storage: indexedDBStorage("my-app"),
});
```

IndexedDB is asynchronous; `sw.ready` resolves after the initial load.

## Node: file

```js
import { fileStorage } from "syncwell-client/storage-node";

const sw = new Syncwell(url, "room", {
  clientId: "worker-1",
  storage: fileStorage(".syncwell/worker-1.json"),
});
```

Writes use a temporary file and rename for atomic replacement.

## Memory and custom adapters

```js
import { MemoryStorage } from "syncwell-client/storage";

const sw = new Syncwell(url, "room", { storage: new MemoryStorage() });
```

An adapter implements `getItem`, `setItem`, and optionally `removeItem`, each of
which may return a value or a promise.

## What happens on reconnect

1. The client sends `hello` with its per-key state vector and only the dirty
   registers written offline.
2. The server merges those registers and replies with `delta` containing only
   registers newer than the vector.
3. The client merges the delta, clears acknowledged dirty keys, and resumes ops.

Set `delta: false` to force the classic full `merge`/`snapshot` exchange. If an
older server never answers `hello`, the SDK falls back automatically.
