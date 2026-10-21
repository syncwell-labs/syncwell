# Syncwell operations guide

This guide describes the safe baseline for a hosted Syncwell deployment. The
developer defaults are intentionally open; production deployments should enable
the controls below.

## Network boundary

- Terminate TLS at a reverse proxy/load balancer (samples in
  [`deploy/`](../deploy/)) or use the built-in server:
  `-tls-cert fullchain.pem -tls-key privkey.pem` (or `SYNCWELL_TLS_CERT` /
  `SYNCWELL_TLS_KEY`). Built-in TLS enforces 1.2+ and serves HTTP/2; certificate
  issuance/renewal stays with your proxy or ACME client. Expose clients through
  `wss://` either way.
- Room tokens require `-secret`/`SYNCWELL_SECRET`. On a non-loopback bind with
  no secret or admin token, the server generates both once and logs them; set
  the env vars for stable credentials. `-insecure` opts out for loopback-only
  local demos.
- Set `-origins https://app.example` so browser WebSocket upgrades are accepted
  only from the expected application origin.
- Keep `/healthz` public for liveness probes and use `/readyz` for readiness;
  `/readyz` returns `503` while the process is shutting down.
- Protect `/stats` and `/metrics` with `-admin-token` or
  `SYNCWELL_ADMIN_TOKEN`; you can also mint a dedicated `admin` capability
  token with `syncwell -secret "$SYNCWELL_SECRET" -mint "*" -mint-caps admin`
  and use it as a Bearer token. Clients should not be able to read room names,
  client counts, GC watermarks, or operational counters.

## Admin and relay protection

`/stats` and `/metrics` go through a per-IP token bucket (default 120
requests/min, burst 30) and a lockout after 10 consecutive `401`s (default
5 minutes). Configure with `-admin-rate`, `-admin-burst`,
`-admin-max-failures`, `-admin-lockout`; disable limiting with a negative rate.
`/cluster/event` is limited separately (`-cluster-rate`, default 6000/min) and
authenticates peers with the admin token.

Restrict either surface at the network layer with CIDRs:

```bash
syncwell ... \
  -admin-allowlist 10.0.0.0/8,192.168.0.0/16 \
  -cluster-allowlist 10.0.0.0/8
```

Behind a trusted reverse proxy, add `-trust-proxy` so limits and lockouts use
the client address from `X-Forwarded-For`. The header is parsed **right-to-left**:
the rightmost `-proxy-hops` entries (default 1) are treated as appended by your
trusted proxies, and the address just before them is taken as the client.
Client-controlled leftmost entries are ignored, so a spoofed `X-Forwarded-For`
cannot bypass rate limits, lockouts, or CIDR allow-lists even behind an
appending proxy such as nginx's default `$proxy_add_x_forwarded_for`. Raise
`-proxy-hops` (or `SYNCWELL_PROXY_HOPS`) for each additional trusted hop
between the internet and this process.

The browser dashboard lives at `/admin`. Its data calls use the same admin
token and guard rails described above. See
[`ADMIN_AND_WEBHOOKS.md`](ADMIN_AND_WEBHOOKS.md) for the room list,
resnapshot/evict actions, and signed `room.created` / `room.persisted` /
`room.evicted` webhooks. Keep `/admin` behind the proxy or an allow-list when
the server is internet-facing; the token protects data, not the HTML shell.

## Observability

`/metrics` exports Prometheus counters, gauges, and histograms (apply latency,
inbound/outbound frame size). Import
[`deploy/grafana/syncwell-dashboard.json`](../deploy/grafana/syncwell-dashboard.json)
and load [`deploy/prometheus/syncwell-rules.yml`](../deploy/prometheus/syncwell-rules.yml);
see [`ALERTS.md`](ALERTS.md).

Traces are optional. Point the OTLP/HTTP exporter at a collector:

```bash
syncwell \
  -otel-endpoint http://otel-collector:4318 \
  -otel-sample-rate 0.1 \
  -otel-headers "x-api-key=$OTLP_KEY"
```

HTTP requests, WebSocket joins, CRDT applies, and cluster posts are traced.
Spans contain room IDs and document keys; keep the collector inside your trust
boundary. `http://` endpoints automatically use plaintext; use HTTPS or
`-otel-insecure` explicitly otherwise.

## External identity (OIDC)

To accept provider-issued JWTs instead of minting HMAC room tokens:

```bash
syncwell \
  -oidc-issuer https://idp.example \
  -oidc-audience syncwell \
  -oidc-room-claim syncwell_room \
  -oidc-caps-claim syncwell_caps
```

The server verifies RS*/PS*/ES* signatures against the provider JWKS
(discovered from the issuer or set with `-oidc-jwks`), requires `iss` and
`exp`, checks `aud` when configured, and refreshes keys every
`-oidc-refresh` (default 15m) and on unknown key IDs. `alg=none` is rejected.

Put the room in `syncwell_room` (or `*`) and capabilities in `syncwell_caps`
(`read`, `write`, `presence`, `admin`). Missing caps default to
`read,write,presence`; `admin` must be explicit. JWTs use the same `?token=`
WebSocket parameter as room tokens and `Authorization: Bearer` on admin
endpoints. If the IdP is unreachable at startup the server still starts but
logs `oidc_jwks_unavailable` and fails closed until keys load. Clustering still
uses the shared `-admin-token`.

## Key rotation

Issue new tokens with the new secret, keep the old secret for verification
during the overlap, then remove it after the longest token TTL:

```bash
# 1. deploy with both (new signs; old still verifies)
syncwell -secret "$NEW" -secret-previous "$OLD" ...
# 2. clients reconnect/mint with tokens signed by $NEW
# 3. after all old tokens expire, redeploy with only -secret "$NEW"
```

`-secret-previous` takes a comma-separated list and is rejected without a
current `-secret`. Tokens are bound to a room and an expiry; revocation is
available for v2 tokens via `-revoked-jtis`.

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

Run with `-data /var/lib/syncwell` on a persistent volume.

**Durability (`-fsync`)**

| mode | survives process kill (`kill -9`) | survives power loss | cost |
| --- | --- | --- | --- |
| `os` / `none` | yes | no | lowest |
| `batch` (default) | yes | yes, within `-fsync-interval` (default 5ms) | low |
| `every` / `always` | yes | yes | one disk flush per write |

Every append is flushed to the OS before the write is acknowledged in all modes;
`batch` merely delays the device flush. Use `-fsync every` when losing the last
few milliseconds of acknowledged writes is unacceptable.

**Log growth (`-max-log-bytes`)**

Each room keeps an append-only op-log that is compacted to a snapshot after
`-compact` writes or once the log reaches `-max-log-bytes` (default 64 MiB,
`0` = unlimited). Compaction is atomic and does not change document state.

**Disk full.** A failed append is rejected: the in-memory document is not
advanced, the client gets an error frame, and the server logs
`room_persist_error`. Compaction failures log `room_compact_error` but the room
keeps serving (the log simply keeps growing until space is freed). Alert on both
events and on volume usage; writes resume automatically once space is available.

**Backup and restore (no filesystem snapshot needed)**

```bash
# Offline: flush + fsync every room, copy logs, write a checksummed manifest.
syncwell -data /var/lib/syncwell -backup /backup/2026-10-20

# Verify sizes and SHA-256 checksums (safe on a live server's copy).
syncwell -verify-backup /backup/2026-10-20

# Restore into an empty or existing data directory (server must be stopped).
syncwell -restore /backup/2026-10-20 -data /var/lib/syncwell
```

`-restore` verifies the manifest before copying anything and refuses a corrupted
backup, so a bad backup cannot overwrite good data. `make restore-drill` runs the
full backup → corruption-rejection → wipe → restore → serve loop in CI.

Keep a copy of legacy pre-v2 room log files until operators have reviewed any
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

## Clustering (full-mesh HA)

Two or more nodes replicate every room to every peer over HTTP. A client can
connect to any node; a write on one is applied, persisted, and broadcast on all
others.

```bash
# node a
syncwell -node-id node-a -admin-token "$ADMIN" \
  -cluster-peers http://node-b:8080 -node-peers node-b -data /var/lib/syncwell

# node b
syncwell -node-id node-b -admin-token "$ADMIN" \
  -cluster-peers http://node-a:8080 -node-peers node-a -data /var/lib/syncwell
```

- `-node-id` is **required** when `-cluster-peers` is set and must be unique.
  The default `"local"` is rejected in cluster mode so two nodes can never
  silently treat each other's events as their own.
- `-node-peers` is the placement view (node ids only) used by the rendezvous
  planner; the owner is shown in `/stats` and logs, and is advisory in
  full-mesh mode.
- `-admin-token` is required when `-secret` is set: peers authenticate their
  event POSTs with it. Keep it identical on every node.
- `-cluster-path` (default `/cluster/event`) is the ingest path.
- `-anti-entropy 30s` (default when clustering) pushes each room's full state to
  peers on an interval, healing events dropped by a full relay queue or a node
  restart. Set `0` to disable.

**Topology and failover.** This is replication, not sharding: every node holds
every room, so any surviving node can serve any room immediately. "Re-homing"
after a node dies means pointing clients (or the load balancer/health check) at a
surviving node — no state transfer is needed because it is already there. The
tradeoff is memory proportional to `nodes × total rooms`; run 2–3 nodes for HA,
not tens. Sharded ownership and an external bus (Redis/NATS) are future work;
see [`COMPLETION_PLAN_2026-10-20.md`](COMPLETION_PLAN_2026-10-20.md) (2.3/2.5).

Cluster metrics on `/metrics`: `syncwell_cluster_events_ingested_total`,
`..._dropped_total`, `syncwell_cluster_relay_{posts,errors,dropped}_total`,
`syncwell_cluster_relay_inflight` (lag proxy), and
`syncwell_cluster_peer_up{peer=...}`.

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
