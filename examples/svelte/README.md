# Svelte example

`syncwellStore.js` is a Svelte 4/5 writable store wrapping one room: read
`$store`, write optimistically with `set`, and call `destroy()` from
`onDestroy` to tear the client down.

```js
import { syncwellStore } from "./syncwellStore";
const doc = syncwellStore({ url, room, clientId });
```

Install Svelte (`npm i svelte`) and this repo's SDK as `syncwell-client`
(`npm install ../../clients/javascript` after `cd clients/javascript && npm run build` — see
[`../README.md`](../README.md)).
