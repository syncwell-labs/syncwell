# Security policy

## Supported versions

Syncwell is pre-1.0. Security fixes land on `main` and in the latest tagged
release; older tags are not maintained. Pin a specific image digest or commit if
you need reproducible deployments.

## Reporting a vulnerability

**Do not open a public issue.** Report privately via GitHub Security Advisories:

https://github.com/Nil-Vaghani/syncwell/security/advisories/new

Please include:

- affected version or commit,
- a description of the impact (what an attacker gains),
- reproduction steps or a proof-of-concept,
- any suggested fix or mitigation.

You can expect an acknowledgement within a few days. Once a fix is available we
will publish an advisory and credit the reporter unless you ask to stay anonymous.

## Scope

In scope:

- the Go server (`cmd/`, `internal/`),
- the JavaScript SDK (`clients/javascript/`),
- the shipped Docker image and deployment docs,
- authentication/authorization, room isolation, and data persistence.

Out of scope:

- denial of service via unbounded resources when you have disabled the built-in
  limits (`-max-message-bytes`, `-max-rooms`, etc.),
- findings that require a compromised host or an already-leaked admin token,
- vulnerabilities in third-party dependencies that have no exploitable path
  through Syncwell (still tell us — Dependabot and CodeQL run on this repo).

## Hardening checklist for operators

- Set `-secret` (or `SYNCWELL_SECRET`) to enable room tokens. On a non-loopback
  bind without it the server generates one and logs it once; set the env var for
  stable credentials across restarts.
- To use an external identity provider, set `-oidc-issuer` (plus
  `-oidc-audience` and custom claim names as needed). JWTs are verified against
  the provider JWKS; grant `admin` only through the caps claim.
- Set `-admin-token` (or `SYNCWELL_ADMIN_TOKEN`) to protect `/stats`, `/metrics`,
  and `/cluster/event`; it is auto-generated on public binds when unset.
- Use `-secret-previous` for zero-downtime room-secret rotation, then remove the
  old secret once tokens have expired.
- Set `-origins` to your app's exact origin(s). Public non-`-insecure` binds now
  fail closed without an Origin policy; `-allow-any-origin` is an explicit
  unsafe override for non-browser clients.
- Webhook URLs must be public `https` endpoints. Private/loopback/metadata
  addresses are blocked at startup, on redirect, and at connect time unless
  `-webhook-allow-private` is set.
- Prefer `Authorization: Bearer` or the `syncwell.token.<token>` WebSocket
  subprotocol over `?token=` to keep credentials out of proxy access logs.
- Keep the admin guard defaults (`-admin-rate`, `-admin-max-failures`,
  `-admin-lockout`) and optionally restrict `/stats`/`/metrics` and
  `/cluster/event` with `-admin-allowlist`/`-cluster-allowlist`.
- Keep `-max-*` limits at their defaults; they bound memory and message size.
- Terminate TLS at a proxy or use built-in `-tls-cert`/`-tls-key`; plain HTTP/WS
  exposes tokens and document data on the wire.
- Back up with `syncwell -data DIR -backup DIR` and verify with
  `-verify-backup`; see `docs/operations/OPERATIONS.md`.
- Read the threat model and residual risks in
  [`docs/operations/THREAT_MODEL.md`](./docs/operations/THREAT_MODEL.md).
