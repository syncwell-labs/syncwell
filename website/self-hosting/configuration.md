# Configuration

All flags have environment equivalents. The most important ones are below; the
full table lives in
[`docs/DEPLOY.md`](https://github.com/Nil-Vaghani/syncwell/blob/main/docs/DEPLOY.md).

## Core

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_ADDR` / `PORT` | `-addr` | `:8080` |
| `SYNCWELL_DATA` | `-data` | in-memory |
| `SYNCWELL_SECRET` | `-secret` | generated once on public binds |
| `SYNCWELL_SECRET_PREVIOUS` | `-secret-previous` | none |
| `SYNCWELL_ADMIN_TOKEN` | `-admin-token` | generated once on public binds |
| `SYNCWELL_ORIGINS` | `-origins` | any origin |
| `SYNCWELL_TRUST_PROXY` | `-trust-proxy` | `false` |
| `SYNCWELL_PROXY_HOPS` | `-proxy-hops` | `1` |
| `SYNCWELL_NODE_ID` | `-node-id` | `local` |
| `SYNCWELL_NODE_PEERS` | `-node-peers` | none |
| `SYNCWELL_CLUSTER_PEERS` | `-cluster-peers` | single node |
| `SYNCWELL_CLUSTER_ALLOWLIST` | `-cluster-allowlist` | any |

## Durability

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_FSYNC` | `-fsync` | `batch` |
| `SYNCWELL_FSYNC_INTERVAL` | `-fsync-interval` | `5ms` |
| `SYNCWELL_MAX_LOG_BYTES` | `-max-log-bytes` | `67108864` |
| `SYNCWELL_BACKUP` / `_VERIFY_BACKUP` / `_RESTORE` | CLI actions | none |

`batch` groups fsyncs for a tiny loss window and higher throughput; `every`
fsyncs each append for power-loss safety at lower throughput.

## Limits

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_MAX_CLIENTS` | `-max-clients` | unlimited |
| `SYNCWELL_MAX_ROOMS` | `-max-rooms` | unlimited |
| `SYNCWELL_MAX_MESSAGE_BYTES` | `-max-message-bytes` | `1048576` |
| `SYNCWELL_MAX_VALUE_BYTES` | `-max-value-bytes` | `262144` |
| `SYNCWELL_MAX_STATE_ENTRIES` | `-max-state-entries` | `10000` |
| `SYNCWELL_MAX_MESSAGES_PER_SECOND` | `-max-messages-per-second` | `120` |

## Security

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_OIDC_ISSUER` | `-oidc-issuer` | disabled |
| `SYNCWELL_OIDC_AUDIENCE` | `-oidc-audience` | no audience check |
| `SYNCWELL_OIDC_CAPS_CLAIM` | `-oidc-caps-claim` | `syncwell_caps` |
| `SYNCWELL_ADMIN_RATE` / `_BURST` | `-admin-rate` / `-admin-burst` | 120/min, burst 30 |
| `SYNCWELL_ADMIN_ALLOWLIST` | `-admin-allowlist` | any |
| `SYNCWELL_TLS_CERT` / `_KEY` | `-tls-cert` / `-tls-key` | proxy TLS |

## Webhooks and tracing

| Variable | Flag | Default |
| --- | --- | --- |
| `SYNCWELL_WEBHOOK_URLS` | `-webhook-urls` | none |
| `SYNCWELL_WEBHOOK_SECRET` | `-webhook-secret` | unsigned |
| `SYNCWELL_WEBHOOK_TIMEOUT` | `-webhook-timeout` | `5s` |
| `SYNCWELL_WEBHOOK_QUEUE` | `-webhook-queue` | `256` |
| `SYNCWELL_OTEL_ENDPOINT` | `-otel-endpoint` | tracing disabled |
| `SYNCWELL_OTEL_SAMPLE_RATE` | `-otel-sample-rate` | `0.1` |

## Tokens

```bash
# Room token (legacy v1) using the current secret
syncwell -secret "$SYNCWELL_SECRET" -mint my-room

# Admin-capability token, e.g. for /stats and /admin
syncwell -secret "$SYNCWELL_SECRET" -mint "*" -mint-caps admin
```

WebSocket clients pass the token as `?token=`. Operational endpoints accept
`Authorization: Bearer <admin-token>`; a JWT with the `admin` capability also
works when OIDC is enabled.
