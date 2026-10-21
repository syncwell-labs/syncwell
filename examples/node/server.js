// Node 22+ server-side client (global WebSocket is built in).
//
//   node examples/node/server.js
//
// Use this for bots, background sync jobs, or an authoritative mirror of a
// room. On Node 18/20 install `ws` and pass `WebSocket` explicitly.
import { Syncwell } from "syncwell-client";
import { fileStorage } from "syncwell-client/storage-node";

const sw = new Syncwell(process.env.SYNCWELL_URL || "ws://localhost:8080/ws", "node-example", {
  clientId: process.env.SYNCWELL_CLIENT_ID || "node-mirror",
  token: process.env.SYNCWELL_TOKEN,
  storage: fileStorage(".syncwell/node-cache.json"),
});

sw.on("open", () => console.log("connected"));
sw.on("close", () => console.log("disconnected; local edits will sync on reconnect"));
sw.on("change", (live) => console.log("document", JSON.stringify(live)));
sw.on("error", (err) => console.error("syncwell:", err.code, err.message));

await sw.ready;
sw.connect();

process.on("SIGINT", () => {
  sw.close();
  process.exit(0);
});
