# Syncwell operations guide

This guide describes the safe baseline for a hosted Syncwell deployment. The
developer defaults are intentionally open; production deployments should enable
the controls below.

## Network boundary

- Terminate TLS at a reverse proxy or load balancer and expose clients through
  `wss://`.
- Set `-origins https://app.example` so browser WebSocket upgrades are accepted
  only from the expected application origin.
- Keep `/healthz` public for liveness probes and use `/readyz` for readiness;
  `/readyz` returns `503` while the process is shutting down.
- Protect `/stats` and `/metrics` with `-admin-token` or
  `SYNCWELL_ADMIN_TOKEN`; you can also mint a dedicated `admin` capability
  token with `syncwell -secret "$SYNCWELL_SECRET" -mint "*" -mint-caps admin`
  and use it as a Bearer token. Clients should not be able to read room names,
  client counts, GC watermarks, or operational counters.

## Room access

Start the server with a room-token secret:

```bash
syncwell -secret "$SYNCWELL_SECRET"
```

Mint room-scoped, expiring tokens from a trusted backend or admin shell:

```bash
syncwell -secret "$SYNCWELL_SECRET" -mint my-room -ttl 24h
```

Pass the token to the browser SDK:

```js
const sw = new Syncwell("wss://sync.example/ws", "my-room", { token });
```

Do not mint tokens in browser code. Room tokens are capability-scoped now:
tokens can carry `read`, `write`, `presence`, and `admin` claims, plus an
optional subject (`sub`) and issuance bounds (`iat`/`nbf`). Use the narrowest
capability set that fits the client’s job.

## Persistence and backup

- Run with `-data /var/lib/syncwell` on a persistent volume.
- Back up the data directory with the service stopped, or after taking a
  filesystem snapshot.
- Restore by stopping the service, restoring the data directory, and restarting
  the same or newer Syncwell binary.
- Keep a copy of legacy pre-v2 room log files until operators have reviewed any
  room IDs that previously mapped to the same sanitized filename.

## Monitoring

Poll `/healthz` for liveness. Poll `/stats` with the admin token for basic room
health:

```bash
curl -H "Authorization: Bearer $SYNCWELL_ADMIN_TOKEN" https://sync.example/stats
```

Watch for unexpectedly high client counts, growing tombstone counts, and
low-water marks stuck at zero. The scrapeable `/metrics` snapshot can be paired
with the alert rules in [`docs/ALERTS.md`](ALERTS.md). Structured logs now emit
JSON events for startup, shutdown, GC, compaction, and persistence failures.

If you are experimenting with the horizontal-scaling foundation, set
`SYNCWELL_CLUSTER_PEERS` or `-cluster-peers` to peer HTTP endpoints and keep the
ingest path on `SYNCWELL_CLUSTER_PATH` or `-cluster-path` (default
`/cluster/event`).

## Resource limits

The server applies conservative default limits to every WebSocket connection:
1 MiB raw frames, 256-byte room IDs, 512-byte document keys, 256-byte node IDs,
256 KiB register values, 64 KiB presence payloads, 10,000 registers per merge,
and 120 messages per client per second.

Tune these for your workload with:

```bash
syncwell \
  -max-message-bytes 1048576 \
  -max-room-id-bytes 256 \
  -max-key-bytes 512 \
  -max-node-bytes 256 \
  -max-value-bytes 262144 \
  -max-presence-bytes 65536 \
  -max-state-entries 10000 \
  -max-messages-per-second 120
```

## Current limitations

- Tokens are still stateless HMAC grants; revocation works by `jti` or secret
  rotation rather than a backing token database.
- Room tokens are room-scoped and capability-scoped, but the system still uses
  shared secrets instead of a full identity-provider integration.
- Tombstone GC still relies on connected-client low-water marks plus stale-
  epoch rejection; there is still no durable per-replica lease registry, so
  operators should keep GC windows conservative for very long offline clients.

## Phase 4 target design

The current implementation already uses principal-aware capabilities:

- `read` for viewing a room
- `write` for mutating state
- `presence` for cursors and ephemeral status
- `admin` for operational endpoints

Token issuance is short-lived by default and revocable by token id (`jti`).
Operationally, rotating the room secret still invalidates previously signed
tokens, which is the recommended rotation path for now.

Tombstone GC should also switch from a pure connected-client low-water mark to a
lease/epoch model. That means:

- connected replicas lease the current room epoch
- stale reconnects are forced to resnapshot or are rejected
- a room can collect tombstones only after the relevant epoch frontier is safe

This is the design target for the remaining Phase 4 work in
[`docs/ACCESS_CONTROL_AND_EPOCHS.md`](ACCESS_CONTROL_AND_EPOCHS.md).
