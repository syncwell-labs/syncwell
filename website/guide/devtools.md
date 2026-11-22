# Visual CRDT DevTools (`syncwell-client/devtools`)

Syncwell includes a built-in browser inspector overlay to inspect live document state, Lamport clocks, tombstones, and real-time operations.

## Mounting the DevTools

```js
import { Syncwell } from "syncwell-client";
import { Awareness } from "syncwell-client/awareness";
import { mountDevTools } from "syncwell-client/devtools";

const sw = new Syncwell("wss://sync.example.com/ws", "room-1");
const awareness = new Awareness(sw);

// Mount the floating debug pill
const devtools = mountDevTools(sw, {
  awareness,
  position: "bottom-right",
});
```

## Features

- **Keyboard Shortcut:** Toggle overlay visibility at any time with `Ctrl+Shift+S` (or `Cmd+Shift+S`).
- **Live State Tab:** Real-time formatted JSON view of `sw.live()` and full CRDT register snapshots.
- **Clocks & Stats Tab:** View logical Lamport counters, active registers, tombstone GC pending counts, and causal ordering explanations.
- **Op Stream Tab:** Chronological audit stream of inbound and outbound frames with timestamp details `(c, n)`.
- **Peers Tab:** Connected remote replicas, their colors, user identities, and active cursor coordinates.
