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
- The supported topology is full mesh. Redis/NATS transport, explicit node
  re-homing, and multi-region failover remain roadmap items.
- Do not run multiple nodes without a shared admin token or with a diverging
  room dataset.

See `docs/operations/DEPLOY.md` and `docs/operations/OPERATIONS.md` for the operational checklist.
