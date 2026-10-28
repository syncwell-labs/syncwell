# Admin dashboard and webhooks

Two operator-facing surfaces ship with the server: a browser dashboard for
inspecting and repairing live rooms, and signed webhooks for reacting to room
lifecycle milestones.

## Admin dashboard

Start the server with an admin token:

```bash
syncwell -admin-token "$SYNCWELL_ADMIN_TOKEN"
# open http://localhost:8080/admin
```

The `/admin` HTML shell is public and contains no room data. Enter the admin
token in the page; it is kept in `sessionStorage` and sent only as
`Authorization: Bearer <token>`. Every data call is authenticated and subject to
the normal admin rate limit, failure lockout, and allow-list.

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/admin` | GET | Dashboard shell |
| `/admin/api/rooms` | GET | Rooms on this node with clients, live keys, tombstones, GC low-water, owner |
| `/admin/api/rooms/{room}/resnapshot` | POST | Force a snapshot broadcast (clients recover from a suspected divergence) |
| `/admin/api/rooms/{room}/evict` | POST | Disconnect clients and remove the room from memory; persisted data is **not** deleted |

Examples:

```bash
curl -H "Authorization: Bearer $SYNCWELL_ADMIN_TOKEN" \
     http://localhost:8080/admin/api/rooms

curl -X POST -H "Authorization: Bearer $SYNCWELL_ADMIN_TOKEN" \
     http://localhost:8080/admin/api/rooms/my-room/resnapshot

curl -X POST -H "Authorization: Bearer $SYNCWELL_ADMIN_TOKEN" \
     http://localhost:8080/admin/api/rooms/my-room/evict
```

`evict` is useful for reclaiming memory from an idle or misbehaving room. A
client that reconnects recreates the room from its durable log. If `/admin` is
publicly reachable, also restrict it at the proxy or use `-admin-allowlist`;
the token protects the data, not the UI shell.

## Webhooks

Webhooks POST a JSON event to one or more HTTP endpoints when a room reaches a
milestone:

| Event | When |
| --- | --- |
| `room.created` | First client joins a room that is not already resident |
| `room.persisted` | A persistent room compacts its first durable snapshot after writes |
| `room.evicted` | A room is evicted for idleness or by the admin action |

```bash
syncwell \
  -webhook-urls "https://hooks.example.com/syncwell" \
  -webhook-secret "$WEBHOOK_SECRET" \
  -webhook-timeout 5s \
  -webhook-queue 256
```

Environment equivalents are `SYNCWELL_WEBHOOK_URLS` (comma-separated),
`SYNCWELL_WEBHOOK_SECRET`, `SYNCWELL_WEBHOOK_TIMEOUT`, and
`SYNCWELL_WEBHOOK_QUEUE`.

Destinations must be public `https` URLs. The server rejects private, loopback,
link-local, carrier-grade NAT, documentation, and cloud-metadata addresses at
startup and re-checks every resolved address at connect time (closing the DNS
rebinding window). Redirects are limited and re-validated; HTTPS-to-HTTP
downgrades are refused. For a trusted receiver on a private network or for
local development, opt in with `-webhook-allow-private` /
`SYNCWELL_WEBHOOK_ALLOW_PRIVATE` and accept the SSRF risk.

Payload:

```json
{
  "event": "room.persisted",
  "room": "my-room",
  "node": "node-a",
  "timestamp": "2026-10-20T12:34:56.789Z",
  "data": { "registers": 42 }
}
```

Headers:

- `X-Syncwell-Event`: the event name.
- `X-Syncwell-Timestamp`: Unix seconds of the event, sent when a secret is
  configured.
- `X-Syncwell-Signature`: `v1=<hex HMAC-SHA256>` over
  `"v1:<timestamp>:<exact request body>"` when a secret is configured. Verify
  with a constant-time comparison and **reject deliveries whose timestamp is
  more than a few minutes from your clock** — this bounds replay of a captured
  request.
- `User-Agent`: `syncwell-webhook/1`.

Delivery semantics:

- Asynchronous and bounded: a slow webhook never blocks a room goroutine.
- At-most-once, no retries. Treat an event as a hint and reconcile by pulling
  room state if the action must be exactly-once.
- Queue overflow drops the event and increments
  `syncwell_webhook_dropped_total`; failed HTTP deliveries increment
  `syncwell_webhook_failed_total`; successful deliveries increment
  `syncwell_webhook_delivered_total`.
- Pending events are discarded on shutdown so termination stays fast.

Receiver checklist (Go):

```go
ts := r.Header.Get("X-Syncwell-Timestamp")
sig := r.Header.Get("X-Syncwell-Signature")
secs, _ := strconv.ParseInt(ts, 10, 64)
if math.Abs(float64(time.Now().Unix()-secs)) > 300 { /* reject */ }
mac := hmac.New(sha256.New, secret)
mac.Write([]byte("v1:" + ts + ":"))
mac.Write(body)
want := "v1=" + hex.EncodeToString(mac.Sum(nil))
if !hmac.Equal([]byte(sig), []byte(want)) { /* reject */ }
```
