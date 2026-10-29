# Clustering

Syncwell supports a full-mesh HTTP event relay for high availability. Each node
still owns its local rooms; cluster events replicate winning register changes and
periodic anti-entropy snapshots heal dropped events.

## Topology

```text
        ┌────────────┐        ┌────────────┐
        │  node-a    │◀──────▶│  node-b    │
        │ rooms A..M  │  HTTP  │ rooms N..Z  │
        └────────────┘        └────────────┘
              ▲                      ▲
              └──── clients ─────────┘
```

A rendezvous shard planner decides an advisory owner for each room. The owner is
reported in `/stats`, logs, and the admin dashboard; clients may still connect to
any node, and events replicate across peers.

## Flags

```bash
syncwell -node-id node-a -admin-token "$ADMIN" \
  -node-peers node-b \
  -cluster-peers https://node-b:8443 \
  -cluster-allowlist 10.0.0.0/8

syncwell -node-id node-b -admin-token "$ADMIN" \
  -node-peers node-a \
  -cluster-peers https://node-a:8443 \
  -cluster-allowlist 10.0.0.0/8
```

`-node-peers` is the placement view (node IDs); `-cluster-peers` is the actual
relay URL list. `-node-id` is required when `-cluster-peers` is set and must be
unique. `-admin-token` is required when `-secret` is set: peers authenticate
relay requests with it. Peer URLs must use `https`; pass `-cluster-insecure`
only for a trusted private network without TLS.

## Guarantees and limits

- Events are delivered over the relay and retried by anti-entropy snapshots.
- A dropped event delays convergence; it does not lose the write permanently.
- The supported relay topology is full mesh. It replicates state locally on
  each node and does not provide exclusive ownership.
- Do not run multiple nodes without a shared admin token or with a diverging
  room dataset.

## Redis ownership mode (experimental)

An opt-in Redis Streams mode provides exclusive per-room leases, fenced writes,
snapshot/replay, `/route`, and opt-in SDK owner following. It is experimental and
is not a production availability guarantee until the crash and provider
failover gates in ADR-001 pass. Redis mode replaces relay mode; do not combine
`-cluster-peers` or `-node-peers` with it.

Every node must use the same Redis database and advertise the same node map.
Redis must use AOF with `appendfsync everysec`; startup checks both settings.
Non-loopback Redis URLs must use `rediss://` and credentials must be passed only
in `SYNCWELL_REDIS_URL`/`-redis-url` (the value is never logged).
The Redis ACL needs `GET`, `SET`, `DEL`, `INCR`, `PEXPIRE`, `XADD`, `XRANGE`,
`XTRIM`, `EVAL`, `EVALSHA`, and `CONFIG GET`; scope key access to `sw:*` where
the provider permits it.

```bash
# Run on each node; change node ID and bind address per node.
syncwell -cluster-mode=redis -redis-url "$SYNCWELL_REDIS_URL" \
  -node-id node-a \
  -node-advertise node-a=wss://node-a.example/ws \
  -node-advertise node-b=wss://node-b.example/ws
```

SDK clients opt in with `followOwner: true` (JavaScript) or
`follow_owner=True` (Python). They call `/route?room=...` before connecting and
again after reconnect. Non-SDK clients receive HTTP 421 with `X-Syncwell-Owner`
and `Location` when they hit a non-owner node. Redis AOF does not prove that a
managed service replicated an acknowledged write or fenced its old primary;
the service topology must guarantee old-primary isolation before promotion.
Do not rely on this mode for production re-homing until that guarantee and the
three-node SIGKILL test are verified. Back up and restore Redis with the
provider's supported Redis backup mechanism, preserving both stream and
snapshot keys. Stop all Syncwell nodes before restoring, then start one node
and verify replay before starting the others. Existing encrypted local room
backups are not a substitute: Redis snapshots and streams are the ownership-
mode source of truth. Test Redis restore separately and record provider backup
retention and encryption policy in the deployment runbook.

See `docs/operations/DEPLOY.md` and `docs/operations/OPERATIONS.md` for the operational checklist.
