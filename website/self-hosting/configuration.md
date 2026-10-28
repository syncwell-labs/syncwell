# Configuration

Every flag has an environment equivalent (`SYNCWELL_` + the flag name in
SHOUTING_SNAKE_CASE) except the one-shot actions (`-mint*`, `-backup`,
`-restore`, `-verify-backup`, `-ttl`, `-version`). The full table lives in
[`docs/operations/DEPLOY.md`](https://github.com/Nil-Vaghani/syncwell/blob/main/docs/operations/DEPLOY.md).

## Core

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_ADDR` / `PORT` | `-addr` | `:8080` |
| `SYNCWELL_DATA` | `-data` | in-memory |
| `SYNCWELL_SECRET` | `-secret` | generated once on public binds |
| `SYNCWELL_SECRET_PREVIOUS` | `-secret-previous` | none |
| `SYNCWELL_ADMIN_TOKEN` | `-admin-token` | generated once on public binds |
| `SYNCWELL_REQUIRE_AUTH` | `-require-auth` | `false` (refuse startup without room auth) |
| `SYNCWELL_ORIGINS` | `-origins` | required on public non-insecure binds |
| `SYNCWELL_REQUIRE_ORIGINS` | `-require-origins` | `true` on public non-insecure binds |
| `SYNCWELL_ALLOW_ANY_ORIGIN` | `-allow-any-origin` | `false` (explicit unsafe override) |
| `SYNCWELL_TRUST_PROXY` | `-trust-proxy` | `false` |
| `SYNCWELL_PROXY_HOPS` | `-proxy-hops` | `1` |
| `SYNCWELL_NODE_ID` | `-node-id` | unset (single node; unique id required when clustering) |
| `SYNCWELL_NODE_PEERS` | `-node-peers` | none |
| `SYNCWELL_CLUSTER_PEERS` | `-cluster-peers` | single node (`https`) |
| `SYNCWELL_CLUSTER_INSECURE` | `-cluster-insecure` | `false` |
| `SYNCWELL_CLUSTER_ALLOWLIST` | `-cluster-allowlist` | any |
| `SYNCWELL_CLUSTER_PATH` | `-cluster-path` | `/cluster/event` |
| `SYNCWELL_CLUSTER_RATE` | `-cluster-rate` | 6000 req/min per IP (negative = unlimited) |
| `SYNCWELL_ANTI_ENTROPY` | `-anti-entropy` | `30s` (0 = disabled) |
| `SYNCWELL_INSECURE` | `-insecure` | `false` (public binds generate credentials) |
| `SYNCWELL_STATIC` / `SYNCWELL_SDK` | `-static` / `-sdk` | `examples/demos` / `clients/javascript` (skipped if missing) |

## Durability

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_FSYNC` | `-fsync` | `batch` |
| `SYNCWELL_FSYNC_INTERVAL` | `-fsync-interval` | `5ms` |
| `SYNCWELL_MAX_LOG_BYTES` | `-max-log-bytes` | `67108864` |
| `SYNCWELL_MIGRATE_LEGACY_V1` | `-migrate-legacy-v1` | `false` |
| `SYNCWELL_COMPACT` | `-compact` | `256` (winning writes between maintenance passes) |
| — (CLI actions, no env) | `-backup` / `-verify-backup` / `-restore` | disabled |

`batch` groups fsyncs for a tiny loss window and higher throughput; `every`
fsyncs each append for power-loss safety at lower throughput.

## Limits

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_MAX_CLIENTS` | `-max-clients` | `256` (0 = unlimited) |
| `SYNCWELL_MAX_ROOMS` | `-max-rooms` | `10000` (0 = unlimited) |
| `SYNCWELL_MAX_MESSAGE_BYTES` | `-max-message-bytes` | `1048576` |
| `SYNCWELL_MAX_VALUE_BYTES` | `-max-value-bytes` | `262144` |
| `SYNCWELL_MAX_STATE_ENTRIES` | `-max-state-entries` | `10000` (per merge/hello frame) |
| `SYNCWELL_MAX_MESSAGES_PER_SECOND` | `-max-messages-per-second` | `120` |
| `SYNCWELL_MAX_DOC_ENTRIES` | `-max-doc-entries` | `100000` registers per room (incl. tombstones) |
| `SYNCWELL_MAX_DOC_BYTES` | `-max-doc-bytes` | `16777216` approx bytes per room |
| `SYNCWELL_MAX_SEND_BYTES` | `-max-send-bytes` | `4194304` queued outbound bytes per client |
| `SYNCWELL_MAX_ROOM_ID_BYTES` | `-max-room-id-bytes` | server default |
| `SYNCWELL_MAX_KEY_BYTES` | `-max-key-bytes` | server default |
| `SYNCWELL_MAX_NODE_BYTES` | `-max-node-bytes` | server default |
| `SYNCWELL_MAX_PRESENCE_BYTES` | `-max-presence-bytes` | server default |
| `SYNCWELL_IDLE_ROOM_TTL` | `-idle-room-ttl` | `60s` (negative disables) |

## Security

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_OIDC_ISSUER` | `-oidc-issuer` | disabled |
| `SYNCWELL_OIDC_AUDIENCE` | `-oidc-audience` | no audience check |
| `SYNCWELL_OIDC_CAPS_CLAIM` | `-oidc-caps-claim` | `syncwell_caps` |
| `SYNCWELL_ADMIN_RATE` / `_BURST` | `-admin-rate` / `-admin-burst` | 120/min, burst 30 |
| `SYNCWELL_ADMIN_ALLOWLIST` | `-admin-allowlist` | any |
| `SYNCWELL_ADMIN_MAX_FAILURES` / `_LOCKOUT` | `-admin-max-failures` / `-admin-lockout` | 10 failures, `5m` lockout |
| `SYNCWELL_WS_RATE` | `-ws-rate` | 300 upgrades/min per IP (negative = unlimited) |
| `SYNCWELL_OIDC_JWKS` | `-oidc-jwks` | discovered from the issuer |
| `SYNCWELL_OIDC_ROOM_CLAIM` | `-oidc-room-claim` | `syncwell_room` |
| `SYNCWELL_OIDC_REFRESH` | `-oidc-refresh` | `15m` |
| `SYNCWELL_REVOKED_JTIS` | `-revoked-jtis` | none |
| `SYNCWELL_TLS_CERT` / `_KEY` | `-tls-cert` / `-tls-key` | none (plain HTTP; terminate TLS at a proxy) |

## Webhooks and tracing

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_WEBHOOK_URLS` | `-webhook-urls` | none |
| `SYNCWELL_WEBHOOK_SECRET` | `-webhook-secret` | unsigned |
| `SYNCWELL_WEBHOOK_TIMEOUT` | `-webhook-timeout` | `5s` |
| `SYNCWELL_WEBHOOK_QUEUE` | `-webhook-queue` | `256` |
| `SYNCWELL_WEBHOOK_ALLOW_PRIVATE` | `-webhook-allow-private` | `false` |
| `SYNCWELL_OTEL_ENDPOINT` | `-otel-endpoint` | tracing disabled |
| `SYNCWELL_OTEL_SAMPLE_RATE` | `-otel-sample-rate` | `0.1` |

## Tokens

```bash
# Room token signed with the current secret (24h default TTL)
syncwell -secret "$SYNCWELL_SECRET" -mint my-room

# Admin-capability token, e.g. for /stats and /admin
syncwell -secret "$SYNCWELL_SECRET" -mint "*" -mint-caps admin
```

WebSocket clients can pass the token as `?token=`, but prefer an
`Authorization: Bearer` header or the `syncwell.token.*` WebSocket subprotocol
to keep credentials out of proxy logs. The JavaScript client opts into the
subprotocol with `tokenTransport: "subprotocol"`. Operational endpoints accept
`Authorization: Bearer`; a JWT with the `admin` capability also works when OIDC
is enabled.

Webhook destinations must be public `https` URLs; private, loopback, and
metadata addresses are rejected (including redirects and DNS rebinding).
