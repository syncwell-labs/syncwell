# React example

`useSyncwell.js` is a drop-in React 18+ hook: one room, one local replica,
optimistic `set()` writes, plus undo/redo and presence.

```jsx
import { useSyncwell } from "./useSyncwell";
const { state, set, undo, redo, status, peers } = useSyncwell({
  url: "wss://sync.example/ws", room: "notes", clientId: "u1",
});
```

Install React (`npm i react`) and this repo's SDK as `syncwell-client`
(`npm install ../../clients/javascript` after `cd clients/javascript && npm run build` — see
[`../README.md`](../README.md)).
