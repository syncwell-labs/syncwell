# Framework recipes

Copy-paste integrations live in the repository under
[`examples/`](https://github.com/Nil-Vaghani/syncwell/tree/main/examples).

## React hook

```js
import { useSyncwell } from "./useSyncwell";

const { state, status, peers, set, undo, redo } = useSyncwell({
  url: "wss://sync.example/ws",
  room: "notes",
  clientId: user.id,
  user: { id: user.id, name: user.name },
});

<input value={state.title || ""} onChange={(e) => set("title", e.target.value)} />
```

The hook connects after local persistence loads, cleans up awareness and the
socket on unmount, and exposes undo/redo state.

## Vue composable

```js
const { state, status, peers, set, undo, cursor } = useSyncwell({
  url: "wss://sync.example/ws",
  room: "notes",
  clientId: user.id,
});
```

## Svelte store

```js
import { createSyncwellStore } from "./syncwellStore";

const room = createSyncwellStore({ url, room: "notes", clientId: user.id });
// $room.state, $room.status, $room.peers
onDestroy(() => room.destroy());
```

## Node bot or mirror

```js
import { Syncwell } from "syncwell-client";
import { fileStorage } from "syncwell-client/storage-node";

const sw = new Syncwell(url, "room", {
  clientId: "mirror",
  storage: fileStorage(".syncwell/mirror.json"),
});
await sw.ready;
sw.connect();
sw.on("change", (live) => mirror(live));
```

See [`examples/`](https://github.com/Nil-Vaghani/syncwell/tree/main/examples)
for the full files, and
[`python/`](https://github.com/Nil-Vaghani/syncwell/tree/main/python) for the
dependency-free Python client.
