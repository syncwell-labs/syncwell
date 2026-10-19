# Deployment guide

Syncwell is a single self-contained binary plus static assets (the demo and the
SDK), so deployment is: build the image, run it with a persistent volume, and
put TLS in front. For the security/operational baseline (TLS termination,
origins, admin token, limits, backup) read
[`docs/OPERATIONS.md`](OPERATIONS.md) first — this file only covers the
platform-specific realities of running the realtime server somewhere.

There is no private module and no build-time token: the engine, SDK, and demos
all live in this repository.

## Build and run

```bash
docker build -t syncwell:local .
docker run --rm -p 8080:8080 \
  -e SYNCWELL_SECRET="$(openssl rand -hex 32)" \
  -e SYNCWELL_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  -e SYNCWELL_ORIGINS="https://your-host.example" \
  -e SYNCWELL_DATA=/var/data/syncwell \
  -v syncwell-data:/var/data \
  syncwell:local
```

The binary reads `PORT` if the platform injects it, and serves:

- `/ws?room=ID` — the sync endpoint
- `/` and `/kanban.html` — demos
- `/sdk/` — the client SDK
- `/healthz`, `/readyz`, `/stats`, `/metrics`

Pin a container tag/commit rather than `latest` for anything beyond a demo.

## Platform realities for realtime

Two constraints bite realtime WebSocket servers on managed platforms:

- **Idle WebSocket timeouts.** Some platforms (Render, in particular) terminate
  idle WebSockets after ~5 minutes on every plan. The SDK reconnects with
  backoff and CRDT convergence still holds, but "a cursor that stays live for an
  hour" is not what you get. Prefer a host that keeps sockets open.
- **Cold starts.** Free tiers that sleep after inactivity add 30 s+ to the first
  request, which makes a realtime demo look broken. Use a non-sleeping tier.

## Host comparison

| Host | Card required? | Persistent WebSockets? | Cold starts? | Notes |
| --- | --- | --- | --- | --- |
| Render free | Yes | No (~5-min idle kill) | Yes (~15 min) | avoid for realtime |
| Render paid | Yes | No (~5-min idle kill) | No | works, with the idle caveat |
| Fly.io | varies | Yes | No | machines + volumes |
| Railway | No (trial) | Yes | No | $5 trial credit |
| Koyeb | No | Yes (limits) | Limited | check current policy |
| Self-host (Hetzner/OVH) | Yes | Yes | No | ~€4/mo, full control |

## Persistent data

Set `SYNCWELL_DATA` to a directory on a persistent volume. On platforms where
only one path persists (for example `/var/data` on Render), point it there and
mount a disk. If you do not need durability across deploys, omit `SYNCWELL_DATA`
and accept in-memory rooms.

Backup and restore are covered in [`docs/OPERATIONS.md`](OPERATIONS.md) and
[`docs/RESTORE_DRILL.md`](RESTORE_DRILL.md).

## Not covered here

- **Horizontal scaling.** The engine is per-process; running multiple instances
  requires the shared transport sketched in
  [`docs/READINESS_AUDIT_2026-10-19.md`](READINESS_AUDIT_2026-10-19.md) (the
  cluster path is not functional yet — see blockers B-1/B-2).
- **Custom domains, autoscaling, backups automation.** These are host features;
  wire them per your provider.
