# Deployment guide

Syncwell is a single self-contained binary plus static assets (the demo and the
SDK), so deployment is: pick a binary/image, run it with a persistent volume,
and terminate TLS (built in or at a proxy). For the security/operational
baseline (origins, admin token, limits, backup) read
[`docs/OPERATIONS.md`](OPERATIONS.md) first — this file only covers the
platform-specific realities of running the realtime server somewhere.

There is no private module and no build-time token: the engine, SDK, and demos
all live in this repository.

## Build and run

```bash
docker build -t syncwell:local .
docker run --rm -p 8080:8080 \
  -e SYNCWELL_SECRET="$(openssl rand -hex 32)" \
  -e SYNCWELL_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  -e SYNCWELL_ORIGINS="https://your-host.example" \
  -e SYNCWELL_DATA=/var/data/syncwell \
  -v syncwell-data:/var/data \
  syncwell:local
```

The binary reads `PORT` if the platform injects it, and serves:

- `/ws?room=ID` — the sync endpoint
- `/` and `/kanban.html` — demos
- `/sdk/` — the client SDK
- `/healthz`, `/readyz`, `/stats`, `/metrics`

Pin a container tag/commit rather than `latest` for anything beyond a demo.

For the public demo/status deployment, pinned-tag verification, docs hosting,
and launch order, use [`LAUNCH.md`](LAUNCH.md).

Ready-to-edit templates:

| Target | File | Notes |
| --- | --- | --- |
| Docker Compose | [`docker-compose.yml`](../docker-compose.yml) | secrets via `.env`, named volume, one-time chown init |
| Fly.io | [`fly.toml`](../fly.toml) | volume + health check; chown the volume once |
| Render | [`render.yaml`](../render.yaml) | disk + generated secrets; chown the disk once |
| nginx | [`deploy/nginx.conf`](../deploy/nginx.conf) | WebSocket upgrade, long read timeout, private relay |
| Caddy | [`deploy/Caddyfile`](../deploy/Caddyfile) | automatic certificates |
| systemd | [`deploy/syncwell.service`](../deploy/syncwell.service) | hardened unit for a binary install |

The image runs as uid/gid `65532` (distroless `nonroot`). Named volumes and
platform disks are usually mounted root-owned, so chown them once:

```bash
# Compose does this automatically via the `init` service; otherwise:
docker run --rm -v syncwell-data:/data busybox chown -R 65532:65532 /data
# Fly:   fly ssh console -u root -C "chown -R 65532:65532 /data"
# Render: chown -R 65532:65532 /var/data   (from the shell)
```

## TLS

Two supported paths:

1. **Terminate at a proxy** (recommended for platform deploys): use
   [`deploy/nginx.conf`](../deploy/nginx.conf) or
   [`deploy/Caddyfile`](../deploy/Caddyfile). WebSockets need
   `proxy_http_version 1.1`, `Upgrade`/`Connection` headers, and a long
   `proxy_read_timeout` (the sample uses 1 h).
2. **Built-in TLS**: mount the certificate and key and pass
   `-tls-cert`/`-tls-key` (or `SYNCWELL_TLS_CERT`/`SYNCWELL_TLS_KEY`):

```bash
syncwell -addr :8443 -data /data \
  -tls-cert /etc/ssl/fullchain.pem -tls-key /etc/ssl/privkey.pem
```

The server enforces TLS 1.2+ and serves HTTP/2. Renewal (Let's Encrypt) is left
to your proxy/ACME client; there is no built-in ACME account storage.

## Credential defaults

Binding a non-loopback address (`:8080`, `0.0.0.0`, a container) without
`-secret`/`-admin-token` generates a 256-bit secret and admin token and prints
them **once** to the log (`auth_secret_generated`, `admin_token_generated`).
They are not persisted, so set `SYNCWELL_SECRET`/`SYNCWELL_ADMIN_TOKEN` for
stable credentials. Mint room tokens with the same secret and pass them to the
client (the demo pages accept `?token=`):

```bash
syncwell -secret "$SYNCWELL_SECRET" -mint demo
# open https://HOST/?token=<token>
```

`-insecure` / `SYNCWELL_INSECURE=1` opts out (loud warning) for a
loopback-only local demo. Do not combine it with a public bind.

Clustering requires an explicit shared `-admin-token`/`SYNCWELL_ADMIN_TOKEN`
(peers authenticate with it); the server refuses to auto-generate one when
`-cluster-peers` is set, because per-node values would not match.

### External identity (OIDC)

Room tokens can also come from an OIDC provider. The server verifies the JWT
signature against the provider's JWKS, then enforces `iss`, `aud` (when
configured), `exp`, and a room claim:

```bash
syncwell \
  -oidc-issuer https://idp.example \
  -oidc-audience syncwell \
  -oidc-room-claim syncwell_room \
  -oidc-caps-claim syncwell_caps
```

- JWKS is discovered from `<issuer>/.well-known/openid-configuration`; override
  with `-oidc-jwks https://idp.example/jwks`.
- Clients pass the JWT exactly like a room token (`?token=` for the WebSocket);
  admin endpoints accept it as `Authorization: Bearer`.
- `syncwell_room` must equal the requested room. A `*` room claim is treated
  as an admin-only credential: it works for admin endpoints
  (`Authorization: Bearer`) but is rejected for room-scoped WebSocket access.
  Capabilities are read from the space/comma-separated `syncwell_caps` claim
  (`read`, `write`, `presence`, `admin`); a missing or empty claim is a
  **deny** — provision the claim in your IdP for every token.
- Signing keys refresh every `-oidc-refresh` (default 15m) and on unknown key
  IDs. If the initial fetch fails the server starts, logs
  `oidc_jwks_unavailable`, and rejects JWTs until a fetch succeeds.
- OIDC is additive: HMAC room tokens still work when `-secret` is set. Cluster
  peers still authenticate with the explicit `-admin-token`.

### Tracing (OTLP/HTTP)

Set `-otel-endpoint` (for example `http://otel-collector:4318`) to export
spans; tracing is off by default. Point it at any OTLP/HTTP collector and add
`-otel-headers "x-api-key=..."` for hosted backends. `-otel-sample-rate`
(default `0.1`) controls sampling. The exporter instruments HTTP requests,
WebSocket joins, CRDT applies, and cluster posts. Spans include room IDs and
document keys, so only send them to a trusted collector.

## Environment variables

| Variable | Flag | Default |
| --- | --- | --- |
| `PORT` / `SYNCWELL_ADDR` | `-addr` | `:8080` |
| `SYNCWELL_DATA` | `-data` | in-memory |
| `SYNCWELL_SECRET` | `-secret` | generated once on public binds |
| `SYNCWELL_SECRET_PREVIOUS` | `-secret-previous` | none (rotation window) |
| `SYNCWELL_ADMIN_TOKEN` | `-admin-token` | generated once on public binds |
| `SYNCWELL_OIDC_ISSUER` | `-oidc-issuer` | empty (OIDC disabled) |
| `SYNCWELL_OIDC_AUDIENCE` | `-oidc-audience` | empty (no audience check) |
| `SYNCWELL_OIDC_JWKS` | `-oidc-jwks` | discovered from the issuer |
| `SYNCWELL_OIDC_ROOM_CLAIM` | `-oidc-room-claim` | `syncwell_room` |
| `SYNCWELL_OIDC_CAPS_CLAIM` | `-oidc-caps-claim` | `syncwell_caps` |
| `SYNCWELL_OIDC_REFRESH` | `-oidc-refresh` | `15m` |
| `SYNCWELL_OTEL_ENDPOINT` | `-otel-endpoint` | empty (tracing disabled) |
| `SYNCWELL_OTEL_INSECURE` | `-otel-insecure` | `false` (implied by `http://`) |
| `SYNCWELL_OTEL_HEADERS` | `-otel-headers` | none |
| `SYNCWELL_OTEL_SAMPLE_RATE` | `-otel-sample-rate` | `0.1` |
| `SYNCWELL_OTEL_SERVICE_NAME` | `-otel-service-name` | `syncwell` |
| `SYNCWELL_ADMIN_RATE` / `_BURST` | `-admin-rate` / `-admin-burst` | 120/min, burst 30 |
| `SYNCWELL_ADMIN_MAX_FAILURES` / `_LOCKOUT` | `-admin-max-failures` / `-admin-lockout` | 10 failures, 5m |
| `SYNCWELL_ADMIN_ALLOWLIST` | `-admin-allowlist` | any |
| `SYNCWELL_CLUSTER_RATE` | `-cluster-rate` | 6000/min |
| `SYNCWELL_CLUSTER_ALLOWLIST` | `-cluster-allowlist` | any |
| `SYNCWELL_TRUST_PROXY` | `-trust-proxy` | `false` (use `X-Forwarded-For` only behind a trusted proxy) |
| `SYNCWELL_PROXY_HOPS` | `-proxy-hops` | `1` (trusted proxies; `X-Forwarded-For` is parsed right-to-left) |
| `SYNCWELL_WEBHOOK_URLS` | `-webhook-urls` | none (webhooks disabled) |
| `SYNCWELL_WEBHOOK_SECRET` | `-webhook-secret` | unsigned webhooks |
| `SYNCWELL_WEBHOOK_TIMEOUT` | `-webhook-timeout` | `5s` |
| `SYNCWELL_WEBHOOK_QUEUE` | `-webhook-queue` | `256` |
| `SYNCWELL_ORIGINS` | `-origins` | any origin |
| `SYNCWELL_FSYNC` | `-fsync` | `batch` |
| `SYNCWELL_FSYNC_INTERVAL` | `-fsync-interval` | `5ms` |
| `SYNCWELL_MAX_LOG_BYTES` | `-max-log-bytes` | `67108864` |
| `SYNCWELL_MAX_CLIENTS` | `-max-clients` | unlimited |
| `SYNCWELL_MAX_ROOMS` | `-max-rooms` | unlimited |
| `SYNCWELL_MAX_MESSAGE_BYTES` | `-max-message-bytes` | server default |
| `SYNCWELL_MAX_MESSAGES_PER_SECOND` | `-max-messages-per-second` | server default |
| `SYNCWELL_IDLE_ROOM_TTL` | `-idle-room-ttl` | `60s` (negative disables) |
| `SYNCWELL_NODE_ID` | `-node-id` | unset (single node) |
| `SYNCWELL_CLUSTER_PEERS` | `-cluster-peers` | single node |
| `SYNCWELL_ANTI_ENTROPY` | `-anti-entropy` | `30s` |
| `SYNCWELL_TLS_CERT` / `SYNCWELL_TLS_KEY` | `-tls-cert` / `-tls-key` | plain HTTP |
| `SYNCWELL_INSECURE` | `-insecure` | `false` (public binds generate credentials) |

## Platform realities for realtime

Two constraints bite realtime WebSocket servers on managed platforms:

- **Idle WebSocket timeouts.** Some platforms (Render, in particular) terminate
  idle WebSockets after ~5 minutes on every plan. The SDK reconnects with
  backoff and CRDT convergence still holds, but "a cursor that stays live for an
  hour" is not what you get. Prefer a host that keeps sockets open.
- **Cold starts.** Free tiers that sleep after inactivity add 30 s+ to the first
  request, which makes a realtime demo look broken. Use a non-sleeping tier.

## Host comparison

| Host | Card required? | Persistent WebSockets? | Cold starts? | Notes |
| --- | --- | --- | --- | --- |
| Render free | Yes | No (~5-min idle kill) | Yes (~15 min) | avoid for realtime |
| Render paid | Yes | No (~5-min idle kill) | No | works, with the idle caveat |
| Fly.io | varies | Yes | No | machines + volumes |
| Railway | No (trial) | Yes | No | $5 trial credit |
| Koyeb | No | Yes (limits) | Limited | check current policy |
| Self-host (Hetzner/OVH) | Yes | Yes | No | ~€4/mo, full control |

## Persistent data

Set `SYNCWELL_DATA` to a directory on a persistent volume. On platforms where
only one path persists (for example `/var/data` on Render), point it there and
mount a disk. If you do not need durability across deploys, omit `SYNCWELL_DATA`
and accept in-memory rooms.

Durability defaults to `-fsync batch` (acknowledged writes survive `kill -9` and
power loss within ~5 ms). Set `SYNCWELL_FSYNC=every` when no acknowledged write
may be lost; set `SYNCWELL_FSYNC=os` for the lowest latency if the volume is
already crash-consistent or the data is reproducible. Room logs compact
automatically at `-max-log-bytes` (default 64 MiB).

Backup and restore are covered in [`docs/OPERATIONS.md`](OPERATIONS.md) and
[`docs/RESTORE_DRILL.md`](RESTORE_DRILL.md).

## Not covered here

- **Horizontal scaling.** Multi-node is supported as full-mesh replication
  (every node holds every room) using `-node-id`, `-node-peers`, and
  `-cluster-peers` — see the Clustering section of
  [`docs/OPERATIONS.md`](OPERATIONS.md). Sharded ownership and an external
  pub/sub backbone are future work.
- **Custom domains, autoscaling, backups automation.** These are host features;
  wire them per your provider.
