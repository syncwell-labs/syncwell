# Operations

## Health and readiness

- `GET /healthz` — process is alive.
- `GET /readyz` — hub accepts realtime traffic; returns `503` during shutdown.

Point load balancers and platform checks at `/healthz`; use `/readyz` for
draining.

## Persistence

With `-data`, each room has an append-only log and periodic snapshots. Records
survive `SIGKILL`; `-fsync every` closes the power-loss window at a throughput
cost. Only a torn final record is tolerated; mid-file corruption fails loudly.

Backups use a SHA-256 manifest and can be verified and restored:

```bash
syncwell -data /data -backup /backups/syncwell.tar
syncwell -verify-backup /backups/syncwell.tar
syncwell -restore /backups/syncwell.tar -data /data
```

Run a restore drill on a copy before relying on it. The repository ships
`make restore-drill` and `docs/RESTORE_DRILL.md`.

## Metrics

`GET /metrics` exports Prometheus counters, gauges, and histograms: connections,
rooms, frame sizes, apply latency, GC, persistence errors, cluster peer health,
webhook delivery, and dropped-event counters. Protect it with the admin token
and optionally `-admin-allowlist`.

A Grafana dashboard, Prometheus alert rules, SLOs, and load-test results live in
the repository under `deploy/`, `docs/ALERTS.md`, `docs/SLO.md`, and
`docs/LOAD_TEST.md`.

## Admin dashboard

Open `/admin` and enter the admin token. The page lists rooms with clients, live
keys, tombstones, GC low-water, and owner. Operators can:

- **Resnapshot** a room to force a full state broadcast.
- **Evict** a room, disconnecting clients and reclaiming memory. Persisted data
  is not deleted; a reconnecting client recreates the room from disk.

The HTML shell is public but contains no data; every API call requires a token
and is rate-limited.

## Webhooks

Set one or more URLs to receive `room.created`, `room.persisted`, and
`room.evicted` events. Bodies are JSON and signed with HMAC-SHA256 when a secret
is configured. Delivery is asynchronous, bounded, and at-most-once; queue drops
and HTTP failures are counted in metrics. See
[Admin and webhooks](/reference/admin-and-webhooks).

## Shutdown

The server drains HTTP, closes WebSocket pumps, stops room loops, flushes durable
stores, and stops webhook workers. Use a termination grace period longer than
your largest in-flight persistence flush.

## Capacity

The repository includes `cmd/loadtest` and a 10k-client / 100-room harness.
Published single-box results are indicative, not an SLA; run your own hours-long
soak before making capacity claims.
