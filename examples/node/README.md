# Node example

A server-side client for bots, background sync jobs, or an authoritative room
mirror (Node 22+ has a global `WebSocket`; on Node 18/20 install `ws` and pass
`WebSocket` explicitly).

```bash
SYNCWELL_URL=ws://localhost:8080/ws node examples/node/server.js
```

It joins the `node-example` room with a file-backed local cache
(`.syncwell/node-cache.json`). `SYNCWELL_CLIENT_ID` and `SYNCWELL_TOKEN`
override the default client id and room token. Install this repo's SDK as
`syncwell-client` first — see [`../README.md`](../README.md).
