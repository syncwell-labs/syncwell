# Syncwell alerts and dashboards

Ready-to-import assets ship in the repo:

- Prometheus rules: [`deploy/prometheus/syncwell-rules.yml`](../deploy/prometheus/syncwell-rules.yml)
- Grafana dashboard: [`deploy/grafana/syncwell-dashboard.json`](../deploy/grafana/syncwell-dashboard.json)

Scrape `/metrics` with the admin token (never a query string):

```yaml
scrape_configs:
  - job_name: syncwell
    scrape_interval: 15s
    authorization:
      credentials: "<SYNCWELL_ADMIN_TOKEN or an admin-capability JWT>"
    static_configs:
      - targets: ["syncwell.internal:8080"]
```

Import the dashboard and pick the Prometheus datasource when prompted. Panels
cover room/client counts, apply latency p50/p95/p99, frame sizes, message
rates, per-room GC low-water, and cluster relay health.

## Metrics reference

| Metric | Type | Meaning |
| --- | --- | --- |
| `syncwell_rooms` | gauge | Live rooms |
| `syncwell_clients_total` | gauge | Connected clients across rooms |
| `syncwell_live_registers_total` | gauge | Live registers |
| `syncwell_tombstones_total` | gauge | Deleted registers awaiting GC |
| `syncwell_room_clients{room}` | gauge | Clients per room |
| `syncwell_room_live{room}` | gauge | Live registers per room |
| `syncwell_room_tombstones{room}` | gauge | Tombstones per room |
| `syncwell_room_low_water{room}` | gauge | GC low-water clock per room |
| `syncwell_apply_duration_seconds` | histogram | Merge + persist time per register |
| `syncwell_inbound_frame_bytes` | histogram | Client message size |
| `syncwell_outbound_frame_bytes` | histogram | Per-client message size |
| `syncwell_cluster_*` | gauge/counter | Peer health, relay posts/errors/drops/inflight |


## Core room health

- `syncwell_rooms == 0` when traffic is expected
  - no rooms are active even though the service is up
- `syncwell_clients_total > threshold`
  - unexpected client growth or a leaked reconnect loop
- `syncwell_tombstones_total` rising steadily
  - GC may be stalled or reconnect leases may be too conservative
- `syncwell_room_low_water{room=...} == 0` for long periods while the room is
  active
  - clients are not advancing their acknowledgements

## Cluster health

- `syncwell_cluster_peer_up{peer=...} == 0` for >5m
  - a peer is unreachable; writes still apply locally and heal via anti-entropy,
    but replication is degraded
- `increase(syncwell_cluster_relay_dropped_total[5m]) > 0`
  - the outbound queue is saturated; peers may be slow or down
- `increase(syncwell_cluster_events_dropped_total[5m]) > 0`
  - inbound events hit the room cap or a full command queue
- `syncwell_cluster_relay_inflight > 32`
  - sustained relay backlog (lag proxy)
- `increase(syncwell_cluster_relay_errors_total[5m]) > 0`
  - peer posts are failing (auth, TLS, or connectivity)

## Highlighted alerts

The shipped rules include latency and correctness alerts that go beyond the
earlier counters:

- `SyncwellApplyLatencyHigh` / `SyncwellApplyLatencyCritical`: apply p95 > 25 ms
  or p99 > 250 ms (disk or room-size pressure).
- `SyncwellFrameSizeHigh`: outbound p99 > 512 KiB.
- `SyncwellLowWaterStuck` / `SyncwellTombstoneGrowth`: acknowledgement or GC
  stalls.
- `SyncwellClusterPeerDown`, `SyncwellClusterRelayErrors`,
  `SyncwellClusterDrops`, `SyncwellClusterRelayBacklog`.

## Operational follow-up

When these alerts fire, inspect:

- `/stats` for room counts and tombstone growth
- `/metrics` for the aggregate room/GC state
- the structured logs for `room_gc`, `room_persist_error`, and
  `shutdown_start`

