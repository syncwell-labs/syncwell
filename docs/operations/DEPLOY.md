# Deployment guide

Syncwell is a single self-contained binary plus static assets (the demo and the
SDK), so deployment is: pick a binary/image, run it with a persistent volume,
and terminate TLS (built in or at a proxy). For the security/operational
baseline (origins, admin token, limits, backup) read
[`docs/operations/OPERATIONS.md`](./OPERATIONS.md) first — this file only covers the
platform-specific realities of running the realtime server somewhere.

There is no private module and no build-time token: the engine, SDK, and demos
all live in this repository.

## Build and run

```bash
docker build -t syncwell:local .
# named volumes start root-owned; chown once before the first start:
docker run --rm -v syncwell-data:/var/data busybox chown -R 65532:65532 /var/data
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
- `/`, `/kanban.html`, `/sequence.html`, and `/demo/lan` — demos
- `/admin` — the admin dashboard
- `/sdk/` — the client SDK
- `/healthz`, `/readyz`, `/stats`, `/metrics`

Pin a container tag/commit rather than `latest` for anything beyond a demo.

For the public demo/status deployment, pinned-tag verification, docs hosting,
and launch order, use [`LAUNCH.md`](../product/LAUNCH.md).

Ready-to-edit templates:

| Target | File | Notes |
| --- | --- | --- |
| Docker Compose | [`docker-compose.yml`](../../docker-compose.yml) | secrets via `.env`, named volume, one-time chown init |
| Render Free | [`render.yaml`](../../render.yaml) | no card, in-memory rooms, sleeps when idle |
| Render paid | [`render.yaml`](../../render.yaml) | add a disk and set `SYNCWELL_DATA`; chown the disk once |
| nginx | [`deploy/proxies/nginx.conf`](../../deploy/proxies/nginx.conf) | WebSocket upgrade, long read timeout, private relay |
| Caddy | [`deploy/proxies/Caddyfile`](../../deploy/proxies/Caddyfile) | automatic certificates |
| systemd | [`deploy/systemd/syncwell.service`](../../deploy/systemd/syncwell.service) | hardened unit for a binary install |

The image runs as uid/gid `65532` (distroless `nonroot`). Named volumes and
platform disks are usually mounted root-owned, so chown them once:

```bash
# Compose does this automatically via the `init` service; otherwise:
docker run --rm -v syncwell-data:/data busybox chown -R 65532:65532 /data
# Render paid disk: chown -R 65532:65532 /var/data   (from the shell)
```

Render Free has no shell or persistent disk, so there is nothing to chown there.

The systemd unit expects `/etc/syncwell.env` (mode `0600`) and starts with
`-require-auth`, so it fails closed if credentials are missing:

```bash
sudo install -m 0600 deploy/systemd/syncwell.env.example /etc/syncwell.env
sudoedit /etc/syncwell.env   # set SYNCWELL_SECRET and SYNCWELL_ADMIN_TOKEN
```

The shipped nginx/Caddy configs keep `/admin` and `/stats`/`/metrics` on the
private network by default; adjust the allow-lists if you administer remotely.
Because the proxy sets `X-Forwarded-For`, the systemd unit also sets
`SYNCWELL_TRUST_PROXY=true` and `SYNCWELL_PROXY_HOPS=1`.

## TLS

Two supported paths:

1. **Terminate at a proxy** (recommended for platform deploys): use
   [`deploy/proxies/nginx.conf`](../../deploy/proxies/nginx.conf) or
   [`deploy/proxies/Caddyfile`](../../deploy/proxies/Caddyfile). WebSockets need
   `proxy_http_version 1.1`, `Upgrade`/`Connection` headers, and a long
   `proxy_read_timeout` (the sample uses 1 h).
2. **Built-in TLS**: mount the certificate and key and pass
   `-tls-cert`/`-tls-key` (or `SYNCWELL_TLS_CERT`/`SYNCWELL_TLS_KEY`):

```bash
syncwell -addr :8443 -data /data \
  -tls-cert /etc/ssl/fullchain.pem -tls-key /etc/ssl/privkey.pem
```

The server enforces TLS 1.3 by default and serves HTTP/2. Use `-tls-min=1.2`
(`SYNCWELL_TLS_MIN=1.2`) only when legacy clients require TLS 1.2. Renewal (Let's Encrypt) is left
to your proxy/ACME client; there is no built-in ACME account storage.

## Credential defaults

Binding a non-loopback address (`:8080`, `0.0.0.0`, a container) without
`-secret`/`-admin-token` generates a 256-bit secret and admin token and prints
them **once** to stderr in a copy-now banner (the structured log records only a
fingerprint: `auth_secret_generated`, `admin_token_generated`).
They are not persisted, so set `SYNCWELL_SECRET`/`SYNCWELL_ADMIN_TOKEN` for
stable credentials. Mint room tokens with the same secret. For SDK clients,
pass the token as the SDK's `token` option; JavaScript uses the WebSocket
subprotocol and Python uses an Authorization header by default. The bundled
browser demo's `?room=...&token=...` URL is a demo-only convention:

```bash
syncwell -secret "$SYNCWELL_SECRET" -mint demo
# Demo-only bundled-page URL; application SDKs should not put tokens in URLs:
# https://HOST/?room=demo&token=<token>
```

`-insecure` / `SYNCWELL_INSECURE=1` opts out of that enforcement: a public
bind may start with no credentials at all, with a loud `INSECURE MODE` banner.
Use it only for a throwaway public demo you knowingly want open — never for a
real deployment. Loopback binds never generate credentials, so the flag has no
effect there.

Clustering requires an explicit shared `-admin-token`/`SYNCWELL_ADMIN_TOKEN`
(peers authenticate with it); the server refuses to auto-generate one when
`-cluster-peers` is set, because per-node values would not match. Peer URLs must
use `https`; if the relay runs on a trusted private network with no TLS, opt in
explicitly with `-cluster-insecure`/`SYNCWELL_CLUSTER_INSECURE` and accept that
the admin token crosses the wire in plaintext.

Public, non-`-insecure` binds now require an explicit Origin policy: set
`-origins` (or `SYNCWELL_ORIGINS`) to the exact `scheme://host[:port]` values
of your browser apps. The historical "any origin" default is available only via
`-allow-any-origin`, intended for non-browser clients. Requests without an
`Origin` header are rejected once an allow-list is configured.

Webhook destinations must be public `https` URLs. Private, loopback, link-local,
and metadata addresses are rejected at startup and again on every resolved
address at connect time (including redirects), preventing DNS rebinding and
SSRF. For a trusted receiver on the local network, set
`-webhook-allow-private` explicitly.

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
- Clients pass the JWT exactly like a room token: preferably
  `Authorization: Bearer` or `Sec-WebSocket-Protocol: syncwell.token.<jwt>`,
  with `?token=` retained only as an explicit compatibility path for older SDKs.
  The bundled demo page's query-token URL is demo-only. Admin endpoints accept
  `Authorization: Bearer` only.
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
| `SYNCWELL_REQUIRE_AUTH` | `-require-auth` | `false` (fail startup without room auth) |
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
| `SYNCWELL_WEBHOOK_ALLOW_PRIVATE` | `-webhook-allow-private` | `false` (SSRF protection) |
| `SYNCWELL_ORIGINS` | `-origins` | none (required on public non-insecure binds) |
| `SYNCWELL_REQUIRE_ORIGINS` | `-require-origins` | `true` on public non-insecure binds |
| `SYNCWELL_ALLOW_ANY_ORIGIN` | `-allow-any-origin` | `false` (explicit unsafe override) |
| `SYNCWELL_FSYNC` | `-fsync` | `batch` |
| `SYNCWELL_FSYNC_INTERVAL` | `-fsync-interval` | `5ms` |
| `SYNCWELL_MAX_LOG_BYTES` | `-max-log-bytes` | `67108864` |
| `SYNCWELL_MIGRATE_LEGACY_V1` | `-migrate-legacy-v1` | `false` (opt-in import of ambiguous v1 filenames) |
| `SYNCWELL_MAX_CONNS` | `-max-conns` | `4096` concurrent WebSockets per process (`0` = unlimited) |
| `SYNCWELL_MAX_CLIENTS` | `-max-clients` | `256` (0 = unlimited) |
| `SYNCWELL_MAX_ROOMS` | `-max-rooms` | `10000` (0 = unlimited; negative = default) |
| `SYNCWELL_MAX_DOC_ENTRIES` | `-max-doc-entries` | `100000` registers per room |
| `SYNCWELL_MAX_DOC_BYTES` | `-max-doc-bytes` | `16777216` approx bytes per room |
| `SYNCWELL_MAX_SEND_BYTES` | `-max-send-bytes` | `4194304` queued bytes per client |
| `SYNCWELL_MAX_MESSAGE_BYTES` | `-max-message-bytes` | server default |
| `SYNCWELL_MAX_MESSAGES_PER_SECOND` | `-max-messages-per-second` | server default |
| `SYNCWELL_MAX_STATE_ENTRIES` | `-max-state-entries` | server default (per merge/hello frame) |
| `SYNCWELL_MAX_ROOM_ID_BYTES` / `_KEY_BYTES` / `_NODE_BYTES` / `_VALUE_BYTES` / `_PRESENCE_BYTES` | `-max-*-bytes` | server defaults |
| `SYNCWELL_IDLE_ROOM_TTL` | `-idle-room-ttl` | `60s` (negative disables) |
| `SYNCWELL_RETENTION` | `-retention` | `0` (disabled; duration such as `720h`) |
| `SYNCWELL_RETENTION_DRY_RUN` | `-retention-dry-run` | `false` |
| `SYNCWELL_SHUTDOWN_TIMEOUT` | `-shutdown-timeout` | `30s` |
| `SYNCWELL_COMPACT` | `-compact` | `256` |
| `SYNCWELL_STATIC` / `SYNCWELL_SDK` | `-static` / `-sdk` | `examples/demos` / `clients/javascript` |
| `SYNCWELL_NODE_ID` | `-node-id` | unset (single node) |
| `SYNCWELL_NODE_PEERS` | `-node-peers` | none (placement by node id) |
| `SYNCWELL_CLUSTER_PEERS` | `-cluster-peers` | single node (https peers required) |
| `SYNCWELL_CLUSTER_INSECURE` | `-cluster-insecure` | `false` (allow http peers on a trusted private network) |
| `SYNCWELL_CLUSTER_PATH` | `-cluster-path` | `/cluster/event` |
| `SYNCWELL_ANTI_ENTROPY` | `-anti-entropy` | `30s` |
| `SYNCWELL_WS_RATE` | `-ws-rate` | 300 upgrades/min per IP |
| `SYNCWELL_REVOKED_JTIS` | `-revoked-jtis` | none |
| `SYNCWELL_TLS_CERT` / `SYNCWELL_TLS_KEY` | `-tls-cert` / `-tls-key` | plain HTTP |
| `SYNCWELL_TLS_MIN` | `-tls-min` | `1.3` (`1.2` is an explicit compatibility opt-in) |
| `SYNCWELL_INSECURE` | `-insecure` | `false` (public binds generate credentials) |

## Platform realities for realtime

Two constraints bite realtime WebSocket servers on managed platforms:

- **Sleeping free tiers.** Render Free spins down after 15 minutes without
  inbound HTTP or WebSocket traffic. The next request waits roughly a minute
  while the service starts, which is noticeable for a public demo.
- **Idle proxy timeouts.** Some proxies terminate quiet WebSockets. The SDK
  reconnects with backoff and CRDT convergence still holds, but prefer a proxy
  with a long read timeout for long-lived cursors.

## Host comparison

| Host | Card required? | Persistent WebSockets? | Cold starts? | Notes |
| --- | --- | --- | --- | --- |
| Render free | No | Yes (while active) | ~1 min after 15 min idle | zero-budget demo; no disk |
| Render paid | Yes | Yes | No | disk + no sleep |
| Railway | No (trial) | Yes | No | $5 trial credit |
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

Backup and restore are covered in [`docs/operations/OPERATIONS.md`](./OPERATIONS.md) and
[`docs/operations/RESTORE_DRILL.md`](./RESTORE_DRILL.md).

## Not covered here

- **Horizontal scaling.** Multi-node is supported as full-mesh replication
  (every node holds every room) using `-node-id`, `-node-peers`, and
  `-cluster-peers` — see the Clustering section of
  [`docs/operations/OPERATIONS.md`](./OPERATIONS.md). Sharded ownership and an external
  pub/sub backbone are future work.
- **Custom domains, autoscaling, backups automation.** These are host features;
  wire them per your provider.
