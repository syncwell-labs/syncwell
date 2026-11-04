# Migration guide for 0.5.0

This release hardens authentication, transport, persistence operations, and
capacity controls. Review these items before upgrading a production server or
client.

## Action required: token transport defaults

The JavaScript client now sends room tokens in the WebSocket subprotocol by
default. The Python client sends `Authorization: Bearer <token>` during the
WebSocket handshake. Both keep tokens out of URLs. Upgrade the server with the
client, or explicitly select a legacy query carrier only when an older server
requires it:

```js
new Syncwell(url, room, { token, tokenTransport: "query" });
```

```python
SyncwellClient(url, room, token=token, allow_query_token=True)
```

Query transport is deprecated and increments a server metric. URLs may be
recorded by proxies and monitoring systems; remove query tokens from logs.

## Action required: TLS floor

The server now requires TLS 1.3 by default. Older TLS 1.2 clients can connect
when the operator explicitly sets `-tls-min 1.2` or
`SYNCWELL_TLS_MIN=1.2`. Plain HTTP local development is unchanged.

## Action required: encrypted backups

New `-backup` commands require an age recipient. Generate and escrow an
identity before scheduling backups; losing it makes encrypted backups
unrecoverable. During key rotation, encrypt to both recipients until restore
with the new identity is proven. Format-1 plaintext backups still verify and
restore. `-backup-plaintext` is an explicit migration-only escape hatch.

## Capacity and operations changes

- The default process-wide WebSocket limit is 4096 (`-max-conns` /
  `SYNCWELL_MAX_CONNS`); `0` disables it. Tune it together with the per-room
  `-max-clients` limit and host file descriptor/memory limits.
- `/sdk/` serves only allow-listed direct SDK assets. Deployments that relied on
  directory listings, nested paths, JSON, or arbitrary extensions must serve
  those assets separately.
- `-retention` defaults to disabled. Review candidates with
  `-retention-dry-run` before enabling destructive cleanup. Admin room purge is
  irreversible without a backup and is recorded in audit/webhook events.
- Graceful shutdown now allows 30 seconds by default. Align
  `-shutdown-timeout` / `SYNCWELL_SHUTDOWN_TIMEOUT` with the process manager's
  termination grace period.
- `/metrics` no longer exports room IDs as labels. Use authenticated `/stats`
  for the live room list and aggregate `/metrics` for monitoring.

## Other behavior changes

- When a room command queue is saturated, the server closes the affected
  WebSocket with code 1013 instead of allowing the request handler to wait
  indefinitely. Clients should reconnect with backoff.
- `-shutdown-timeout` / `SYNCWELL_SHUTDOWN_TIMEOUT` controls the bounded room
  drain and final persistence flush. The default is 30 seconds; incomplete
  drains are logged and should be treated as an unclean shutdown.
- Admin purge emits an audit event and a `room.purged` webhook. Audit events go
  to stderr by default; configure `-audit-log` for an append-only mode-0600
  JSONL file, and include that file in log rotation and retention policy.
- Request IDs are validated or generated, echoed as `X-Request-ID`, and added
  to request logs and traces. Send a safe 8–64 character ID to correlate an
  operation across your proxy and Syncwell.
- Query-token compatibility remains available only by explicit SDK opt-in.
  Server use is rate-limited in logs and counted by
  `syncwell_token_in_query_total`; the token itself is never logged.
- Cluster relay peer URLs must be absolute host URLs without a path, query, or
  user information. Redirects are rejected; outbound requests bypass proxies,
  use a two-second timeout, and require TLS 1.2 or newer. Plain HTTP peers
  require the explicit `-cluster-insecure` option.
- Room metadata sidecars are created alongside logs and included in backups.
  Descriptor and storage metrics are aggregate; room IDs are not metric labels.
- The server now exports request/connection rejection, open-log, process FD,
  goroutine, heap, relay, anti-entropy, and compaction metrics. Review the
  supplied alerts and set `GOMEMLIMIT` to match the container memory budget.

## Organization and deployment

Current repository links move to `syncwell-labs/syncwell`. On Render, update the
container image only after the new owner's image digest is published and an
anonymous pull succeeds. Keep the existing digest until that cutover is ready.

## Upgrade sequence

1. Generate and store an age identity in an offline recovery location.
2. Verify the current backup and take an encrypted backup with the new binary.
3. Set compatible TLS policy and capacity flags in deployment configuration.
4. Upgrade server and SDK together; use explicit query-token compatibility only
   for clients that cannot yet upgrade.
5. Run health, two-client convergence, encrypted restore, and monitoring checks.
6. Enable retention only after dry-run candidates and restore procedures have
   been reviewed.

See [operations](./OPERATIONS.md), [alerts/SLOs](./ALERTS.md), and the
[release checklist](./RELEASE_CHECKLIST.md).
