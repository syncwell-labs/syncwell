# Framework examples

Each example is intentionally small: create a `Syncwell` client, read from the
local replica, and write optimistically. The SDK handles reconnects, offline
merging, persistence, undo/redo, and awareness.

These are copy-paste files, not scaffolded apps — drop the relevant file into
your project. The SDK is served by the engine at `/sdk/`, or install it into
your own bundle: `syncwell-client` is this repo's [`clients/javascript/`](../clients/javascript) package, so
install it from the repo until it is published to npm:

```bash
cd clients/javascript && npm install && npm run build && cd ..   # build once (dist/ is not committed)
npm install ./clients/javascript                                  # or "syncwell-client": "file:./clients/javascript" in package.json
```

| Example | File | Needs |
| --- | --- | --- |
| React hook | [`react/useSyncwell.js`](react/useSyncwell.js) | React 18+ |
| Vue 3 composable | [`vue/useSyncwell.js`](vue/useSyncwell.js) | Vue 3 |
| Svelte store | [`svelte/syncwellStore.js`](svelte/syncwellStore.js) | Svelte 4/5 |
| Vanilla | [`vanilla/index.html`](vanilla/index.html) | any static server with `/sdk/` |
| Node server | [`node/server.js`](node/server.js) | Node 22+ |

The vanilla page needs the SDK served at `/sdk/`; run it from a syncwell
server's static root or copy `clients/javascript/` next to it. The Svelte store exposes
`destroy()`; call it from `onDestroy`.
