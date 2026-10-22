# Service level objectives

These are the targets Syncwell is designed and tested against for a **single
node**. They are engineering SLOs, not a commercial SLA; multi-region
deployments and per-tenant fairness are out of scope (see the
[threat model](./THREAT_MODEL.md)).

Reference host used for the published numbers: Linux x86-64, 16 vCPU, 13 GiB
RAM, loopback networking, in-memory store. Your disk and network will dominate
once persistence or TLS is enabled.

## Objectives

| Area | SLI | SLO | Budget basis (30 days) |
| --- | --- | --- | --- |
| Availability | `up{job="syncwell"}` / `/healthz` success | 99.9% | 43m 12s downtime |
| WebSocket admission | Authorized `/ws` upgrades that succeed (non-5xx) | 99.9% | 43m 12s of failed admissions |
| Apply latency | `syncwell_apply_duration_seconds` p95 | ≤ 25 ms | ≤ 1% of 5-minute windows over target |
| Apply tail | `syncwell_apply_duration_seconds` p99 | ≤ 250 ms | ≤ 1% of 5-minute windows over target |
| End-to-end fan-out | publish → receive p95, 10k clients | ≤ 100 ms | ≤ 1% of 5-minute windows over target |
| Durability (fsync=every) | Acknowledged writes lost on power loss | 0 | no loss permitted |
| Durability (fsync=batch) | Acknowledged writes lost in the last 5 ms | ≤ 5 ms window | planned, documented |
| Cluster convergence | State convergence after peer recovery | ≤ 60 s p95 | ≤ 1% of 5-minute windows over target |
| Memory | Server RSS with 10k clients / 100 rooms | ≤ 512 MiB | none (capacity guard) |
| Capacity | Sustained fan-out on the reference host | ≥ 100k msg/s | none (capacity guard) |

## How to measure

```promql
# Availability over the month
avg_over_time(up{job="syncwell"}[30d])

# Apply latency
histogram_quantile(0.95, sum(rate(syncwell_apply_duration_seconds_bucket[5m])) by (le))
histogram_quantile(0.99, sum(rate(syncwell_apply_duration_seconds_bucket[5m])) by (le))

# Fan-out throughput
sum(rate(syncwell_outbound_frame_bytes_count[5m]))

# Memory (from the host, not Prometheus)
grep VmRSS /proc/$(pgrep -f 'syncwell -addr')/status
```

Alerts for these objectives ship in
[`deploy/monitoring/prometheus/syncwell-rules.yml`](../../deploy/monitoring/prometheus/syncwell-rules.yml).
Load methodology and measured results are in [LOAD_TEST.md](./LOAD_TEST.md).

## Durability semantics

- `-fsync every`: every accepted write is synced before it is acknowledged.
  No acknowledged write is lost to power loss.
- `-fsync batch` (default): writes are acknowledged after the append and synced
  every 5 ms. A power loss can lose the last few milliseconds of acknowledged
  writes. Replication does not change this window on a node.
- `-fsync os`: the OS decides when to flush; suitable only for caches and
  development.

Back up with `-backup` (checksummed manifest) and test restores with
`-verify-backup`; see [RESTORE_DRILL.md](./RESTORE_DRILL.md).

## Error budget policy

- At 75% budget consumed: feature work pauses; prioritize reliability fixes.
- At 100%: only reliability/security changes ship until the budget recovers.
- Availability budgets exclude announced maintenance windows; apply-latency
  budgets do not.
- Cluster convergence is best-effort eventual consistency: anti-entropy runs
  every 30 s by default (`-anti-entropy`), so RPO is bounded by that interval,
  not zero.
