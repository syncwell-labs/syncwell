# Syncwell Wire Protocol (v0.2, Phase 2)

Transport: **WebSocket**, one connection per client. Endpoint:

```
GET /ws?room=<ROOM_ID>[&token=<TOKEN>]   (ROOM_ID defaults to "default")
```

All frames are **JSON text** messages. The protocol is intentionally tiny.
Frames may include `"protocol": 1`; clients and servers reject unsupported
non-zero protocol versions.

Default server limits reject pathological input before it reaches room state
(each can be tuned with the matching `-max-*` flag):
room IDs are capped at 256 bytes, keys and node IDs at 512 and 256 bytes,
register values at 256 KiB, presence payloads at 64 KiB, merge states at 10,000
entries, raw WebSocket messages at 1 MiB, and each client connection at 120
messages per second.

**Auth (optional).** If the server was started with a `-secret`, the connection
must carry a valid room `token`. The server accepts the legacy
`<exp>.<hmac-sha256(secret, "room|exp")>` form, the older
`<exp>.<caps>.<jti>.<hmac-sha256(...)>` form, and the current versioned
principal-aware `v2.<base64url(JSON claims)>.<hmac-sha256(secret, "room|v2|payload")>`
format. Tokens can carry `read`, `write`, `presence`, and `admin`
capabilities; the `admin` capability is used for privileged endpoints such as
`/stats` and `/metrics`. A non-allow-listed `Origin` is rejected with `403`.
With no secret configured, the endpoint is open (dev mode).

As of Phase 2 the **client authors the timestamp** (each client holds its own
CRDT replica and Lamport clock). The server is a merge-and-fan-out relay, not a
sequencer. This is what makes offline editing possible.

A **register** is `{ "v": <value>, "t": { "c": <counter>, "n": <node> }, "d"?: true }`.

Registers are ordered first by timestamp `(c,n)`. If a malformed or buggy
client produces different registers with the same timestamp, the implementations
use a deterministic content tie-break (with a tombstone preferred over a live
value). This preserves convergence; well-behaved clients must still never reuse
a timestamp for a different write.

---

## Client → Server

### `op` — a single write (already stamped by the client)
```json
{ "type": "op", "key": "title", "register": { "v": "Hello", "t": { "c": 8, "n": "c-1a2b" } } }
```
A delete is the same frame with a tombstone register: `{ "t": {...}, "d": true }`.
The server merges the register into the room CRDT and broadcasts the winner.
When persistence is enabled, a winning write is broadcast only after the append
succeeds; on append failure the server returns an `error` frame instead of
acknowledging an undurable change.

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

### `error` — operation or connection rejected
```json
{ "type": "error", "data": "persistence failed" }
```
The server may send this before closing an over-capacity room connection or when
a winning write could not be persisted.

---

## Idempotency & ordering guarantees

- Applying any `op` or `snapshot` is **idempotent**: re-delivery cannot corrupt
  state because the underlying merge is a CRDT join.
- A client may **reconnect at any time**; the `snapshot` it receives reflects all
  writes the server had committed, so no acknowledgement/replay protocol is
  required in Phase 1.

## HTTP endpoints

- `GET /healthz` → `ok`
- `GET /readyz` → `ok` while the hub accepts realtime traffic; `503` during
  shutdown
- `GET /stats` → JSON map of `room → { clients, live, tombstones, lowWater }`
  (`Authorization: Bearer <admin-token>` is required when
  `-admin-token` / `SYNCWELL_ADMIN_TOKEN` is configured; a Bearer token with
  `admin` capability also works when the room secret is enabled)
- `GET /metrics` → Prometheus-style snapshot of the same operational counters
  with the same admin-capability or admin-token protection
