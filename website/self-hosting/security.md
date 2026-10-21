# Security

## Safe defaults

On a public bind without `-secret`/`-admin-token`, the server generates both and
logs them once. Clustering requires an explicit shared admin token. Use
`-insecure` only for local experiments; it opts out of generated credentials
with a warning.

## Room tokens and capabilities

Tokens are stateless HMAC grants scoped to a room and expiring. Capabilities are
`read`, `write`, `presence`, and `admin`. HMAC room tokens minted with an empty
capability list default to `read,write,presence` and never include `admin`.
OIDC/JWT tokens fail closed: a missing or empty caps claim is a deny, and a
`*` room claim only works for admin endpoints.

Rotate the room secret with `-secret-previous`: the current secret signs, the
previous secret still verifies during the window.

## OIDC/JWT

Enable `-oidc-issuer` to accept provider-issued JWTs. The server verifies
`iss`/`aud`/`exp`, refreshes JWKS, and maps room/capability claims. Failed
discovery is fail-closed at startup. Grant `admin` explicitly in the caps claim.

## Origin and network controls

- Set `-origins` to an allow-list for browser clients.
- Put `/stats`, `/metrics`, `/admin`, and `/cluster/event` behind an allow-list
  or private network.
- Enable `-trust-proxy` only behind trusted proxies. `X-Forwarded-For` is
  parsed right-to-left: the rightmost `-proxy-hops` entries (default 1) are
  yours; client-controlled leftmost entries are ignored.
- Use TLS at the proxy or with `-tls-cert`/`-tls-key`.

## Admin hardening

The admin endpoints use per-IP token buckets, consecutive-failure lockout, and
CIDR allow-lists. `/admin` itself serves only a static shell; room data requires
the token on every API request. The token is kept in `sessionStorage` in the
browser and sent only as an `Authorization` header.

## Reporting

Do not open a public issue for vulnerabilities. Use the repository's private
security advisory flow described in `SECURITY.md`. The threat model, including
residual risks and non-goals, is in `docs/THREAT_MODEL.md`.
