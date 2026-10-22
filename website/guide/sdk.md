# JavaScript SDK

`syncwell-client` is ESM/CJS with hand-maintained TypeScript declarations and no
runtime dependencies.

::: tip Installing
`syncwell-client` publishes to npm with each release tag. Until then, copy the
`clients/javascript/` directory into your project (or import from your server's `/sdk/` mount)
— the API below is identical.
:::

## Constructor

```ts
import { Syncwell } from "syncwell-client";

const sw = new Syncwell<{ title: string; done: boolean }>(
  "wss://sync.example/ws",
  "room-1",
  {
    token,                 // optional room token
    clientId: "user-42",   // stable id; required for useful persistence
    persist: true,         // localStorage adapter
    storage,               // or a custom adapter
    storageKey: "my-app:room-1",
    delta: true,           // state-vector reconnect (default)
    historyLimit: 100,     // undo groups kept in memory
    WebSocket,             // inject in Node < 22 or tests
    maxReconnectDelay: 8000,
  },
);
```

`sw.ready` resolves after the initial persistence load. Connect after it when you
need the UI hydrated first:

```js
await sw.ready;
sw.connect();
```

## Methods

| Method | Description |
| --- | --- |
| `connect()` / `close()` | Open/close the socket; reconnect is automatic unless closed |
| `set(key, value)` / `delete(key)` | Optimistic local write, then sync |
| `get(key)` / `live()` | Read the local replica |
| `batch(fn)` | One change event and one undo step for many writes |
| `undo()` / `redo()` | Per-client history; refuses to clobber newer remote writes |
| `canUndo()` / `canRedo()` | History state |
| `clearHistory()` | Drop undo and redo stacks |
| `presence(data)` | Ephemeral broadcast |
| `on(event, cb)` / `off(event, cb?)` | Subscribe/unsubscribe |

Events: `open`, `close`, `change`, `presence`, `leave`, `error`, `history`.

## Errors

`error` handlers receive `SyncwellError` with a `code`:

`NO_WEBSOCKET`, `BAD_FRAME`, `SERVER_ERROR`, `STALE_EPOCH`,
`UNSUPPORTED_PROTOCOL`, `STORAGE_READ`, `STORAGE_WRITE`.

```js
sw.on("error", (err) => {
  console.error(err.code, err.message, err.cause);
});
```

## Builds and entry points

| Entry | Use |
| --- | --- |
| `syncwell-client` | ESM/CJS client |
| `syncwell-client/text` | `SequenceText`, `rankBetween` |
| `syncwell-client/awareness` | Typed presence/cursors |
| `syncwell-client/storage` | Memory, localStorage, IndexedDB |
| `syncwell-client/storage-node` | Atomic Node file adapter |
| `syncwell-client/crdt` | Bare `LWWMap`, `Clock`, `tsAfter` |
| `syncwell-client/sequence` | Bare sequence document |
| `syncwell-client/browser` | Minified IIFE (`SyncwellSDK`) |

## TypeScript

```ts
const sw = new Syncwell<{ title: string; done: boolean }>(url, "room");
sw.on("change", (live) => live.title.toUpperCase());
sw.set("done", true);       // ok
// sw.set("done", "yes");   // type error
```
