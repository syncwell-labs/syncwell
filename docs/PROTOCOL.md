# Syncwell Wire Protocol (v0.2, Phase 2)

Transport: **WebSocket**, one connection per client. Endpoint:

```
GET /ws?room=<ROOM_ID>[&token=<TOKEN>]   (ROOM_ID defaults to "default")
```

All frames are **JSON text** messages. The protocol is intentionally tiny.

**Auth (optional).** If the server was started with a `-secret`, the connection
must carry a valid room `token` (`"<exp>.<hmac-sha256(secret, "room|exp")>"`),
or the upgrade is rejected with `401`. A non-allow-listed `Origin` is rejected
with `403`. With no secret configured, the endpoint is open (dev mode).

As of Phase 2 the **client authors the timestamp** (each client holds its own
CRDT replica and Lamport clock). The server is a merge-and-fan-out relay, not a
sequencer. This is what makes offline editing possible.

A **register** is `{ "v": <value>, "t": { "c": <counter>, "n": <node> }, "d"?: true }`.

---

## Client → Server

### `op` — a single write (already stamped by the client)
```json
{ "type": "op", "key": "title", "register": { "v": "Hello", "t": { "c": 8, "n": "c-1a2b" } } }
```
A delete is the same frame with a tombstone register: `{ "t": {...}, "d": true }`.
The server merges the register into the room CRDT and broadcasts the winner.

### `merge` — full client state, sent on (re)connect
```json
{ "type": "merge", "state": { "title": { "v": "Hello", "t": { "c": 8, "n": "c-1a2b" } } } }
```
This is how edits made **while offline** propagate. The server merges the whole
state, broadcasts any new winners to the other clients, and replies to the
sender with a fresh `snapshot` so it converges with everything it missed. A
fresh client with no local state skips this frame.

### `presence` — ephemeral broadcast (not persisted)
```json
{ "type": "presence", "data": <any JSON> }
```
Relayed to all *other* clients as a `presence` message. Used for cursors,
selections, and who's-online. Never stored, never replayed on join.

### `ack` — advertise the client's logical clock (GC heartbeat)
```json
{ "type": "ack", "clock": 81 }
```
Sent on connect and on a periodic heartbeat. Lets the server advance the
tombstone-GC low-water mark even for idle clients. No response.

---

## Server → Client

### `snapshot` — full state, sent once on join
```json
{
  "type": "snapshot",
  "state": {
    "title": { "v": "Hello", "t": { "c": 7, "n": "server:demo" } },
    "old":   { "t": { "c": 3, "n": "server:demo" }, "d": true }
  }
}
```
`state` maps each key to its **register**: `v` value, `t` timestamp
(`c` counter, `n` node), `d` tombstone flag. A fresh snapshot is also what a
client receives after reconnecting — this is how missed updates are recovered.

### `op` — a single committed change
```json
{ "type": "op", "key": "title", "register": { "v": "Hello", "t": { "c": 8, "n": "server:demo" } } }
```
A tombstone op carries `"register": { "t": {...}, "d": true }`.

### `presence` — relayed ephemeral data
```json
{ "type": "presence", "clientId": "c4", "data": { "cursor": { "x": 12, "y": 40 } } }
```

### `leave` — a client disconnected
```json
{ "type": "leave", "clientId": "c4" }
```

---

## Idempotency & ordering guarantees

- Applying any `op` or `snapshot` is **idempotent**: re-delivery cannot corrupt
  state because the underlying merge is a CRDT join.
- A client may **reconnect at any time**; the `snapshot` it receives reflects all
  writes the server had committed, so no acknowledgement/replay protocol is
  required in Phase 1.

## HTTP endpoints

- `GET /healthz` → `ok`
- `GET /stats` → JSON map of `room → { clients, live, tombstones, lowWater }`
