# Syncwell alert rules

These are the minimum alerts that make the new `/metrics` endpoint useful in
practice.

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

## Example Prometheus rules

```yaml
groups:
  - name: syncwell
    rules:
      - alert: SyncwellNoRoomsActive
        expr: syncwell_rooms == 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: Syncwell is healthy but no rooms are active

      - alert: SyncwellClientsSurge
        expr: syncwell_clients_total > 500
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: Syncwell client count is unusually high

      - alert: SyncwellLowWaterStuck
        expr: syncwell_room_low_water == 0
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: A room is not advancing GC low-water marks
```

## Operational follow-up

When these alerts fire, inspect:

- `/stats` for room counts and tombstone growth
- `/metrics` for the aggregate room/GC state
- the structured logs for `room_gc`, `room_persist_error`, and
  `shutdown_start`

