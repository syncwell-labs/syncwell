# Syncwell service objectives and runbook

These initial objectives apply to one production Syncwell process. Collect
`/metrics` through Prometheus using the admin authorization header; never put
the admin token in a URL. They are operating targets, not a contractual SLA.

## Objectives

| Objective | Target | Measurement |
| --- | --- | --- |
| Process scrape availability | 99.9% over 30 days | `avg_over_time(up{job="syncwell"}[30d])` |
| Register apply latency | p95 below 25 ms and p99 below 250 ms in 5 minute windows | `syncwell_apply_duration_seconds_bucket` |
| Connection capacity | No sustained connection-budget rejects | `increase(syncwell_connection_rejects_total[5m])` |
| File descriptor headroom | Stay below 80% of the process soft limit | `syncwell_process_open_fds / syncwell_process_fd_limit` |
| Heap headroom | Stay below 80% of `GOMEMLIMIT`, when configured | `syncwell_heap_bytes / syncwell_gomemlimit_bytes` |
| Persistence health | No compaction failures; acknowledged writes remain durable per configured fsync mode | `syncwell_compaction_failures_total`, `room_persist_error`, and restart/restore drills |
| Relay health (cluster deployments) | No sustained peer failures or drops | `syncwell_cluster_peer_up`, `syncwell_cluster_relay_errors_total{peer,reason}`, `syncwell_cluster_relay_dropped_total` |
| Redis ownership (experimental) | No lease loss; replay p95 < 5 s | `syncwell_redis_lease_losses_total`, `syncwell_redis_stream_errors_total`, `syncwell_redis_replay_duration_seconds` |

The scrape objective measures whether Prometheus can reach `/metrics`; it does
not prove every WebSocket session works. Use browser and restore drills to
cover client behavior and persistence. Relay and anti-entropy objectives apply
only when peers are configured.

## Runbook

- **Connection rejects**: inspect `syncwell_connections_current` and
  `syncwell_connections_limit`. Raise `-max-conns` / `SYNCWELL_MAX_CONNS` only
  after checking host FD and memory headroom. The limit is process-wide; also
  check `-max-clients` per room.
- **FD alert**: inspect `syncwell_process_open_fds`, `syncwell_open_log_files`,
  and the OS soft limit. Raise `ulimit -n`/service `LimitNOFILE`, then check
  whether idle rooms close descriptors and whether clients are reconnecting.
- **Heap alert**: compare `syncwell_heap_bytes` to `syncwell_gomemlimit_bytes`,
  check room/client counts, and set `GOMEMLIMIT` below the container limit to
  reserve space for stacks and non-Go allocations.
- **Persistence/compaction error**: inspect disk space, permissions, and
  `-fsync` policy. `room_persist_error` means a winning write was rejected;
  `room_compact_error` means logs may grow until compaction succeeds. Verify the
  latest encrypted backup before maintenance.
- **Shutdown incomplete**: inspect `shutdown_incomplete` room counts and
  `shutdown_room_flush_error`; increase `-shutdown-timeout` only if the process
  manager grants the same termination grace period.
- **Room command queue full / WebSocket 1013**: clients should reconnect with
  backoff. Check `syncwell_command_queue_rejects_total`, goroutine/heap load,
  and slow storage. Do not increase queue depth as the first response.
- **Retention candidates**: begin with `-retention-dry-run`; review
  `retention_would_purge`, then enable `-retention` only after the window and
  backup restore are confirmed.
- **Relay peer down/errors**: check the peer URL, TLS, admin token, and network;
  inspect latency/error reason labels. Full-state anti-entropy remains the
  recovery path; `syncwell_anti_entropy_bytes_total` shows its traffic.

See [alerts and dashboards](./ALERTS.md), [operations](./OPERATIONS.md), and
[deployment flags](./DEPLOY.md) for the matching metric names and configuration.
