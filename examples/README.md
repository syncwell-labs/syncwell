# Framework examples

Each example is intentionally small: create a `Syncwell` client, read from the
local replica, and write optimistically. The SDK handles reconnects, offline
merging, persistence, undo/redo, and awareness.

These are copy-paste files, not scaffolded apps — drop the relevant file into
your project. The SDK is served by the engine at `/sdk/` (or copy the `sdk/`
directory from this repo; `npm install syncwell-client` works once the package
is published).

| Example | File | Needs |
| --- | --- | --- |
| React hook | [`react/useSyncwell.js`](react/useSyncwell.js) | React 18+ |
| Vue 3 composable | [`vue/useSyncwell.js`](vue/useSyncwell.js) | Vue 3 |
| Svelte store | [`svelte/syncwellStore.js`](svelte/syncwellStore.js) | Svelte 4/5 |
| Vanilla | [`vanilla/index.html`](vanilla/index.html) | any static server with `/sdk/` |
| Node server | [`node/server.js`](node/server.js) | Node 22+ |

The vanilla page needs the SDK served at `/sdk/`; run it from a syncwell
server's static root or copy `sdk/` next to it. The Svelte store exposes
`destroy()`; call it from `onDestroy`.
