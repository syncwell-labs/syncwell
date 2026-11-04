# Admin and webhooks

## Admin dashboard

`GET /admin` serves a static shell with no room data. Enter the admin token; it
is kept in `sessionStorage` and sent only as
`Authorization: Bearer <token>`.

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/admin/api/rooms` | GET | Room list with clients, live/tombstone counts, GC low-water, owner |
| `/admin/api/rooms/{room}/resnapshot` | POST | Force a full snapshot broadcast |
| `/admin/api/rooms/{room}/evict` | POST | Drop the room from memory; persisted data remains |

```bash
curl -H "Authorization: Bearer $SYNCWELL_ADMIN_TOKEN" \
  https://sync.example/admin/api/rooms
```

`evict` disconnects clients and reclaims memory. The next client recreates the
room from its durable log. Rate limits, failure lockout, CIDR allow-listing, and
proxy-level protection apply to all data APIs.

## Webhooks

Configure one or more URLs:

```bash
syncwell \
  -webhook-urls "https://hooks.example.com/syncwell" \
  -webhook-secret "$WEBHOOK_SECRET" \
  -webhook-timeout 5s \
  -webhook-queue 256
```

| Event | When |
| --- | --- |
| `room.created` | First client joins a non-resident room |
| `room.persisted` | A persistent room compacts its first durable snapshot after writes |
| `room.evicted` | Idle or admin eviction |

```json
{
  "event": "room.persisted",
  "room": "my-room",
  "node": "node-a",
  "timestamp": "2026-10-20T12:34:56.789Z",
  "data": { "registers": 42 }
}
```

Headers: `X-Syncwell-Event`, `X-Syncwell-Timestamp` (Unix seconds),
`X-Syncwell-Signature: v1=<hex HMAC-SHA256>` over `"v1:<timestamp>:<body>"`, and
`User-Agent: syncwell-webhook/1`. Verify the HMAC with a constant-time
comparison and reject deliveries whose timestamp is more than a few minutes
from your clock (this bounds replay).

Delivery is asynchronous, bounded, and **at-most-once**. There are no retries;
queue overflow and HTTP failures increment metrics. Treat an event as a hint and
reconcile state if the action must be exactly-once.

Full detail:
[`docs/operations/ADMIN_AND_WEBHOOKS.md`](https://github.com/syncwell-labs/syncwell/blob/main/docs/operations/ADMIN_AND_WEBHOOKS.md).
