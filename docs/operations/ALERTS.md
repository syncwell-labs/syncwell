# Syncwell alerts and dashboards

Ready-to-import assets ship in the repo:

- Prometheus rules: [`deploy/monitoring/prometheus/syncwell-rules.yml`](../../deploy/monitoring/prometheus/syncwell-rules.yml)
- Grafana dashboard: [`deploy/monitoring/grafana/syncwell-dashboard.json`](../../deploy/monitoring/grafana/syncwell-dashboard.json)

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
cover aggregate room/client counts, apply latency p50/p95/p99, frame sizes,
message rates, process saturation, and cluster relay health. Metric labels do
not include room IDs, client IDs, or document keys.

## Metrics reference

| Metric | Type | Meaning |
| --- | --- | --- |
| `syncwell_rooms` | gauge | Live rooms |
| `syncwell_clients_total` | gauge | Connected clients across rooms |
| `syncwell_live_registers_total` | gauge | Live registers |
| `syncwell_tombstones_total` | gauge | Deleted registers awaiting GC |
| `syncwell_rooms_low_water_stuck` | gauge | Rooms with clients and no advancing acknowledgement watermark |
| `syncwell_connections_current` / `_limit` | gauge | Current sockets and process limit |
| `syncwell_connection_rejects_total` | counter | Sockets rejected by the connection budget |
| `syncwell_open_log_files` / `syncwell_open_log_bytes` | gauge | Persistent log descriptors and log bytes |
| `syncwell_process_open_fds` / `_fd_limit` | gauge | Process descriptors and soft limit (Linux; zero elsewhere) |
| `syncwell_goroutines` / `syncwell_heap_bytes` | gauge | Go runtime goroutines and heap allocation |
| `syncwell_gomemlimit_bytes` | gauge | Runtime `GOMEMLIMIT` (zero means unlimited) |
| `syncwell_cluster_relay_duration_seconds{peer}` | histogram | Relay delivery latency by configured peer |
| `syncwell_cluster_relay_errors_total{peer,reason}` | counter | Relay failures by configured peer and bounded reason |
| `syncwell_anti_entropy_bytes_total` | counter | Bytes sent in full-snapshot anti-entropy |
| `syncwell_compaction_failures_total` | counter | Failed room-log compactions |
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
- `syncwell_rooms_low_water_stuck > 0` for long periods
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
- `SyncwellFileDescriptorsHigh`, `SyncwellHeapNearMemoryLimit`,
  `SyncwellConnectionRejects`, and `SyncwellCompactionFailures` cover process
  saturation and persistence health.
- `SyncwellRedisLeaseLoss` and `SyncwellRedisStreamErrors` are critical in
  experimental Redis mode; `SyncwellRedisReplaySlow` warns when replay p95 is
  above five seconds. These signals have no room labels.

Set `GOMEMLIMIT` below the container memory limit to leave room for stacks,
runtime metadata, and non-Go allocations. The dashboard compares heap against
the configured runtime limit; the binary does not set this value.

## Operational follow-up

When these alerts fire, inspect:

- `/stats` for room counts and tombstone growth
- `/metrics` for the aggregate room/GC state
- the structured logs for `room_gc`, `room_persist_error`, and
  `shutdown_start`

