# Changelog

## Unreleased

### Breaking changes

- The JavaScript SDK now sends room tokens through the WebSocket subprotocol by
  default, and the Python SDK sends them in an `Authorization: Bearer` header.
  Both SDKs keep credentials out of the URL by default. Set
  `tokenTransport: "query"` in JavaScript or `allow_query_token=True` in Python
  only when connecting to a legacy server that requires query tokens.
- **Action required:** TLS now defaults to a TLS 1.3 minimum. Set
  `-tls-min 1.2` / `SYNCWELL_TLS_MIN=1.2` for clients that require TLS 1.2.
- **Action required:** `-backup` now requires `-backup-recipient age1...` and
  writes encrypted backups. Existing format-1 plaintext backups remain
  restorable; `-backup-plaintext` is an explicit migration-only option.
- **Action required:** `/metrics` no longer exposes room IDs in metric labels;
  use authenticated `/stats` for per-room inspection.

### Security and operations

- `/sdk/` now serves only direct, regular SDK assets from an extension
  allow-list; directories, symlinks, dotfiles, traversal, and other extensions
  return 404. Root static serving also rejects dotfiles and directories without
  an index instead of exposing listings. **Action required:** move any custom
  nested or non-allow-listed SDK assets to a separate static host.
- Added a default process-wide cap of 4096 WebSocket connections with 503
  admission responses; `-max-conns 0` remains unlimited. **Action required:**
  raise the cap if a single process must serve more than 4096 concurrent clients.
- Added encrypted room purge, age-based retention controls (disabled by
  default), room metadata sidecars, audit events, request IDs, and
  `room.purged` webhooks.
- Query-token use remains an explicit compatibility path; the server emits a
  rate-limited deprecation event and `syncwell_token_in_query_total` without
  recording token values.
- Added a 30 second graceful shutdown budget, room queue overload close code
  1013, and persistence descriptor accounting. **Action required:** align
  `-shutdown-timeout` with the process manager's termination grace period.
- Added aggregate process, connection, storage, anti-entropy, and relay
  metrics, saturation alerts, and a Grafana dashboard.
- The HTTP cluster relay exchanges per-register digests in bounded pages,
  sends bidirectional missing-register deltas, and chunks oversized snapshot
  fallbacks to the event body limit.
- Cluster relay requests now use a dedicated no-proxy transport, a 2 second
  timeout, TLS 1.2 minimum, and reject redirects. Peer URLs must be absolute
  host URLs; plain HTTP requires `-cluster-insecure`. **Action required:**
  remove paths/query strings from peer URLs and explicitly allow insecure HTTP
  only for trusted private networks.
- Added generated/validated `X-Request-ID` propagation into response headers,
  server spans, request logs, and room-admin command logs.
- Added weekly scheduled fuzzing and nightly dedicated-runner load/soak jobs.
- Added pinned GitHub Actions, documentation link checks, browser coverage, and
  a `make vuln` target for govulncheck.
- Current repository and package ownership moves to `syncwell-labs`; Render
  image cutover requires the newly published digest.

See [`MIGRATION_0.5.0.md`](docs/operations/MIGRATION_0.5.0.md) before upgrading.
