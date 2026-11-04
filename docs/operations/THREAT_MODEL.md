# Threat model

Scope: the Syncwell server (`cmd/syncwell`), the Go CRDT/room engine, and the
JavaScript client SDK in this repository. The demos are reference clients, not a
security boundary.

## Assets

| Asset | Where it lives | Impact if lost |
| --- | --- | --- |
| Room document state | process memory + `-data` logs/snapshots | data loss, integrity loss |
| Room tokens | HMAC-signed, held by clients | unauthorized read/write/presence |
| Admin token / admin-capability tokens | operator secret, `Authorization` header | room enumeration, metrics, cluster injection |
| Cluster relay | `/cluster/event` (private network) | forged writes/presence in any room |
| Availability | room goroutines, sockets, disk | denial of service |
| Backups | operator storage (`-backup`) | data loss if stolen or corrupted |

## Actors and trust boundaries

```
 untrusted internet
        │  wss (TLS at proxy or -tls-cert)
        ▼
 ┌──────────────────────────┐      private network      ┌─────────────────┐
 │ reverse proxy / LB       │◀─────────────────────────▶│ peer Syncwell   │
 └────────────┬─────────────┘   /cluster/event + token  └─────────────────┘
              │
              ▼
 ┌──────────────────────────┐
 │ Syncwell node            │──▶ disk (`-data`, backups)
 │  /ws /stats /metrics     │
 └──────────────────────────┘
```

- **Anonymous client**: can reach `/ws`, `/healthz`, `/readyz`; without a valid
  room token it cannot read or write when a secret is configured.
- **Room client**: holds a token for one room with `read`/`write`/`presence`
  capabilities. It is trusted to author its own operations only.
- **Operator**: holds the admin token or an admin-capability token, controls
  configuration, disk, and the cluster network.
- **Peer node**: trusted to relay events; authenticates with the admin token.
- **Network attacker**: can observe/modify traffic if TLS is not used.

## Threats and mitigations

| Threat | Mitigation in this repo | Residual risk |
| --- | --- | --- |
| Anonymous room access | `-secret`/OIDC required on public binds (auto-generated if absent); every `/ws` upgrade verifies a room token or IdP JWT with `read` capability | Open by design when the operator uses `-insecure` or explicitly disables auth on loopback |
| Token forgery | HMAC-SHA256 signatures over room + expiry (+ v2 payload), constant-time compare; OIDC JWTs verify RS*/PS*/ES* signatures against the provider JWKS (`alg=none` rejected) | Secret compromise (HMAC) or IdP compromise (OIDC); HMAC secrets are symmetric |
| Token replay after revocation | `-revoked-jtis` denies listed v2 token ids; short `-ttl` for minted tokens | v1 (legacy) tokens have no JTI |
| OIDC misconfiguration / key rollover | `iss` and `exp` are always enforced, `aud` when configured; room/cap claims are checked on every upgrade; JWKS refreshes every 15m and on unknown `kid`; discovery/JWKS failure fails closed | A compromised IdP can mint tokens; custom claims must be provisioned by the provider |
| Stale token after key rotation | `-secret-previous` accepts old secrets while the current secret signs new tokens; validation refuses previous-only configs | Old secrets must be removed once tokens expire |
| Admin brute force | Per-IP token bucket on `/stats` and `/metrics`, consecutive-failure lockout (`-admin-max-failures`, `-admin-lockout`), optional CIDR allow-list | Behind a proxy without `-trust-proxy`, all clients share the proxy IP; with `-trust-proxy`, client IPs come from the rightmost `-proxy-hops` entries of `X-Forwarded-For` (leftmost client-controlled entries are ignored) |
| Unauthenticated admin endpoints | Public binds auto-generate `-admin-token`; admin endpoints accept only `Authorization: Bearer` (never query strings) | Loopback/`-insecure` deployments remain open by operator choice |
| Forged cluster events | `/cluster/event` uses the admin auth path; peers send the admin token; optional peer CIDR allow-list + rate limit; peer URLs must be `https` unless `-cluster-insecure` is explicit | A compromised peer or admin token can write to any room |
| SSRF via operator-configured URLs | Webhooks must be public `https`; loopback/private/link-local/metadata ranges are rejected at startup, on every redirect, and again at dial after re-resolving the host (closing the DNS-rebinding window); environment proxies are disabled. OIDC issuer/JWKS URLs use the same policy with a loopback-only plaintext exception for development | A trusted private receiver requires the explicit `-webhook-allow-private` opt-in and accepts the SSRF risk |
| Cross-site request / WebSocket abuse | Exact Origin allow-list on upgrades and admin actions; public non-`-insecure` binds fail closed without `-origins`; a missing Origin is rejected once a list is set; default ports and host case are canonicalized; `-allow-any-origin` is an explicit unsafe opt-in | Origin headers are not an authentication boundary; a non-browser client can omit or forge them, but still needs a valid token |
| TLS downgrade / browser hardening | TLS 1.3 minimum by default (`-tls-min=1.2` is an explicit compatibility opt-in), HSTS when served over TLS, and CSP/`X-Frame-Options`/`nosniff`/`Referrer-Policy`/`Permissions-Policy` on every response | Headers do not protect against a compromised proxy or TLS terminator |
| Message/CPU/disk exhaustion | Read limits (`-max-message-bytes`), per-client rate limit, room/key/value/presence/state caps, per-room document entry/byte budgets (`-max-doc-entries`/`-max-doc-bytes`), per-client outbound queue budget (`-max-send-bytes`), max rooms/clients, idle eviction, log compaction (`-max-log-bytes`) | Application-level floods within the configured caps |
| Silent room cross-contamination from legacy logs | The v1 sanitized filename layout was not injective; importing it is opt-in (`-migrate-legacy-v1`) and unsafe room ids are refused. Corrupt or over-budget persisted state is rejected without overwriting the original log | Operators who opt in must verify that legacy ids are unambiguous |
| Oversized or malformed frames | `json.Unmarshal` failures are ignored; `validateInbound` enforces protocol and limits; fuzz targets cover inbound frames, cluster events, token parsing, canonical JSON; seed corpora run as unit tests on every `go test` | Dedicated `-fuzz` campaigns are manual (not scheduled in CI); parser bugs could still surface under longer fuzzing |
| Log injection / secret leakage | Structured JSON logs; admin tokens accepted only via `Authorization: Bearer`. Room tokens accept `Authorization: Bearer`, the `Sec-WebSocket-Protocol: syncwell.token.<token>` carrier, or the legacy `?token=` (prefer the first two); the shipped nginx log format records `$uri`, not `$request_uri`; generated credentials are printed once to stderr and structured logs carry only a SHA-256 fingerprint | Aggregated stderr still captures the one-time print; legacy room tokens in URLs leak via proxy logs and browser history |
| Telemetry leakage | Tracing is opt-in; exporters only send to an operator-configured endpoint; sampling defaults to 10% | Spans include room IDs and keys; the collector is part of the trust boundary |
| Data at rest theft | None built in: the data directory is plaintext JSON logs; OS/volume encryption is the operator's responsibility | Disk access = document access |
| Backup tampering/partial copies | `-backup` writes a SHA-256 manifest; `-verify-backup` and `-restore` verify sizes and hashes; restore refuses unsafe paths | Backups are not encrypted or signed |
| Supply-chain compromise | Pinned Go modules (`gorilla/websocket`, `golang-jwt/jwt/v5`, OpenTelemetry SDK/exporters and their transitive modules), govulncheck + CodeQL + Dependabot in CI, SDK has no runtime dependencies | Dev tooling (esbuild/TypeScript, CI actions) is still supply chain |
| XSS in demos | Demo renders user text through `textContent`/escaping helpers; no `innerHTML` with document values | Forks can reintroduce unsafe rendering |
| Cross-room isolation | Rooms are separate goroutines/documents; tokens are bound to a room (wildcard only for admin capability) | A bug in routing/merge could cross rooms; no per-tenant quotas beyond caps |

## Explicitly out of scope (today)

- **End-to-end encryption / zero-knowledge server**: the server can read all
  document state.
- **Malicious server or malicious operator**: they control the data and logs.
- **Multi-tenant billing/quota isolation**: limits are global/per-room, not
  per-account.
- **DDoS at the edge**: use a proxy/CDN with L3/L4 protection; the in-process
  limits are not a DDoS defense.

## Reporting

See [`SECURITY.md`](../../SECURITY.md). Please do not open public issues for
vulnerabilities.
