# Wire protocol

Syncwell uses one WebSocket per client and small JSON text frames. The normative
v1 reference is
[`docs/protocol/PROTOCOL.md`](https://github.com/syncwell-labs/syncwell/blob/main/docs/protocol/PROTOCOL.md);
this page is the fast orientation.

```text
GET /ws?room=<ROOM_ID>[&delta=1]
```

`ROOM_ID` defaults to `default`. Frames may include `"protocol": 1`.
Authenticate with `Authorization: Bearer <TOKEN>` or the
`syncwell.token.<TOKEN>` WebSocket subprotocol. The legacy `token` query
parameter remains supported, but SDKs require an explicit opt-in to use it.

## Registers

```json
{ "v": "Hello", "t": { "c": 8, "n": "client-a" }, "d": false }
```

A missing `d` or `d: false` is a live value; `d: true` is a tombstone. Order by
`(c, n)` and use the frozen deterministic content tie-break for equal stamps.

## Client → server

| Type | Purpose |
| --- | --- |
| `op` | One stamped register write |
| `merge` | Full client state on reconnect; server replies with `snapshot` |
| `hello` | State vector + optional offline regs; server replies with `delta` |
| `presence` | Ephemeral JSON relayed to other clients |
| `ack` | Advertise the client's clock for tombstone GC |

```json
{ "type": "op", "key": "title",
  "register": { "v": "Hello", "t": { "c": 8, "n": "c-1" } } }
```

```json
{ "type": "hello",
  "vector": { "title": { "c": 8, "n": "c-1" } },
  "state": { "offline-key": { "v": 1, "t": { "c": 9, "n": "c-1" } } } }
```

## Server → client

| Type | Purpose |
| --- | --- |
| `snapshot` | Full state on join or after `merge` |
| `delta` | Only registers newer than the client's vector |
| `op` | One committed change |
| `presence` | Relayed presence with `clientId` |
| `leave` | A peer disconnected |
| `error` | Rejected operation or connection |

`delta` carries `epoch`. If compaction advanced past the client's vector, the
server rejects the `hello` as a stale epoch and the client resnapshots.

## Guarantees

- Every apply is idempotent and a CRDT join.
- Duplicate/reordered delivery cannot corrupt state.
- A reconnect needs no replay log: snapshot or delta reflects committed state.
- Presence is never persisted or replayed.
- With persistence enabled, a winning write is acknowledged only after append.
- Delta is opt-in on the URL and falls back to snapshot/merge.

## HTTP

`/healthz`, `/readyz`, `/stats`, `/metrics`, `/admin`, and `/cluster/event`.
Operational endpoints require the admin token/capability where configured.

See [Compatibility policy](/reference/compatibility) before changing any frame.
