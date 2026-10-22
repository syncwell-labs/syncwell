# Vue 3 example

`useSyncwell.js` is a Vue 3 composable: one room, one local replica,
optimistic writes, undo/redo, and awareness peers.

```js
import { useSyncwell } from "./useSyncwell";
const { state, status, peers, set, undo, redo } = useSyncwell({ url, room, clientId });
```

Install Vue (`npm i vue`) and this repo's SDK as `syncwell-client`
(`npm install ../../clients/javascript` after `cd clients/javascript && npm run build` — see
[`../README.md`](../README.md)).
