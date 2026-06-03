# Syncwell Wire Protocol (v0, Phase 1)

Transport: **WebSocket**, one connection per client. Endpoint:

```
GET /ws?room=<ROOM_ID>     (ROOM_ID defaults to "default")
```

All frames are **JSON text** messages. The protocol is intentionally tiny.

---

## Client → Server

### `set` — write a value
```json
{ "type": "set", "key": "title", "value": <any JSON> }
```
The server stamps the write with the room's logical clock, applies it to the
CRDT, and broadcasts an `op` to every client.

### `delete` — tombstone a key
```json
{ "type": "delete", "key": "title" }
```

### `presence` — ephemeral broadcast (not persisted)
```json
{ "type": "presence", "data": <any JSON> }
```
Relayed to all *other* clients as a `presence` message. Used for cursors,
selections, and who's-online. Never stored, never replayed on join.

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

## Planned protocol changes (Phase 2)

- A client→server `merge` frame carrying client-side CRDT state, to support
  offline editing (client holds its own clock and document).
- A low-water-mark `ack` frame to drive tombstone garbage collection.
