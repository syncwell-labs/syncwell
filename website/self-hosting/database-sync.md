# Database Sync (PostgreSQL & Supabase)

While Syncwell maintains high-performance, append-only persistence on disk for real-time document convergence, production systems often need to synchronize the collaborative state into primary relational databases (such as **PostgreSQL**, **Supabase**, or **SQLite**) for analytics, reporting, full-text search, and relational foreign keys.

Syncwell supports this through signed **room milestone webhooks** paired with the **Admin State Export API**.

---

## Architecture Overview

```
┌──────────────────┐               ┌────────────────────────┐
│  Browser Clients │               │ Syncwell Engine        │
│  (Live Edits)    │──────WS──────▶│ • Append-only Log      │
└──────────────────┘               │ • CRDT Convergence    │
                                   └───────────┬────────────┘
                                               │
                               1. Webhook      │ 2. Admin API
                               room.persisted  │ GET /admin/api/rooms/{id}/state
                                               ▼
                                   ┌────────────────────────┐
                                   │ Your Backend Receiver  │
                                   │ (Node.js / Python / Go)│
                                   └───────────┬────────────┘
                                               │
                                               │ 3. UPSERT INTO rooms
                                               ▼
                                   ┌────────────────────────┐
                                   │ PostgreSQL / Supabase  │
                                   │ state JSONB            │
                                   └────────────────────────┘
```

---

## 1. PostgreSQL Schema

Create a table to store the collaborative room documents:

```sql
CREATE TABLE collaborative_documents (
    room_id TEXT PRIMARY KEY,
    state JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index on JSONB for high-speed attribute queries:
CREATE INDEX idx_collab_state ON collaborative_documents USING GIN (state);
```

---

## 2. Server Configuration

Start Syncwell with `-webhook-urls` and `-webhook-secret` to receive milestone notifications:

```bash
syncwell \
  -addr :8080 \
  -secret "$SYNCWELL_SECRET" \
  -admin-token "$SYNCWELL_ADMIN_TOKEN" \
  -webhook-urls http://backend-service:3000/webhooks/syncwell \
  -webhook-secret "$WEBHOOK_SECRET"
```

Syncwell fires signed HTTP POST requests with header:
```http
X-Syncwell-Signature: sha256=<HMAC_HEX>
```
Whenever a room is created, persisted to disk (`room.persisted`), or evicted (`room.evicted`).

---

## 3. Node.js & TypeScript Webhook Receiver

```ts
import express from "express";
import crypto from "crypto";
import { Pool } from "pg";

const app = express();
app.use(express.json());

const pool = new Pool({ connectionString: process.env.DATABASE_URL });
const SYNCWELL_URL = process.env.SYNCWELL_URL || "http://localhost:8080";
const ADMIN_TOKEN = process.env.SYNCWELL_ADMIN_TOKEN!;
const WEBHOOK_SECRET = process.env.SYNCWELL_WEBHOOK_SECRET!;

// Verify HMAC-SHA256 signature
function verifySignature(payload: string, signatureHeader?: string): boolean {
  if (!signatureHeader) return false;
  const expected = "sha256=" + crypto.createHmac("sha256", WEBHOOK_SECRET).update(payload).digest("hex");
  return crypto.timingSafeEqual(Buffer.from(signatureHeader), Buffer.from(expected));
}

app.post("/webhooks/syncwell", async (req, res) => {
  const rawBody = JSON.stringify(req.body);
  if (!verifySignature(rawBody, req.headers["x-syncwell-signature"] as string)) {
    return res.status(401).send("Invalid signature");
  }

  const { event, room } = req.body;

  // Sync state whenever the room is persisted or evicted from RAM
  if (event === "room.persisted" || event === "room.evicted") {
    try {
      // Fetch live document JSON from Syncwell Admin API
      const resp = await fetch(`${SYNCWELL_URL}/admin/api/rooms/${room}/state`, {
        headers: { Authorization: `Bearer ${ADMIN_TOKEN}` },
      });

      if (!resp.ok) {
        console.error(`Failed to export room ${room}: ${resp.statusText}`);
        return res.sendStatus(500);
      }

      const { state } = await resp.json();

      // Upsert into PostgreSQL
      await pool.query(
        `INSERT INTO collaborative_documents (room_id, state, updated_at)
         VALUES ($1, $2, NOW())
         ON CONFLICT (room_id)
         DO UPDATE SET state = $2, updated_at = NOW();`,
        [room, JSON.stringify(state)]
      );

      console.log(`Synced room ${room} to PostgreSQL`);
    } catch (err) {
      console.error(`Error syncing room ${room}:`, err);
      return res.sendStatus(500);
    }
  }

  res.sendStatus(200);
});

app.listen(3000, () => console.log("Webhook receiver listening on port 3000"));
```

---

## 4. Python / FastAPI Receiver

```python
import hmac
import hashlib
import json
import httpx
from fastapi import FastAPI, Request, Header, HTTPException
import asyncpg

app = FastAPI()
db_pool = None

SYNCWELL_URL = "http://localhost:8080"
ADMIN_TOKEN = "your-admin-token"
WEBHOOK_SECRET = b"your-webhook-secret"

@app.on_event("startup")
async def startup():
    global db_pool
    db_pool = await asyncpg.create_pool("postgresql://postgres:pass@localhost:5432/mydb")

@app.post("/webhooks/syncwell")
async def handle_syncwell_webhook(
    request: Request,
    x_syncwell_signature: str = Header(None)
):
    body = await request.body()
    expected_sig = "sha256=" + hmac.new(WEBHOOK_SECRET, body, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(x_syncwell_signature or "", expected_sig):
        raise HTTPException(status_code=401, detail="Invalid signature")

    data = json.loads(body)
    event, room = data.get("event"), data.get("room")

    if event in ("room.persisted", "room.evicted"):
        async with httpx.AsyncClient() as client:
            resp = await client.get(
                f"{SYNCWELL_URL}/admin/api/rooms/{room}/state",
                headers={"Authorization": f"Bearer {ADMIN_TOKEN}"}
            )
            if resp.status_code == 200:
                state = resp.json().get("state", {})
                async with db_pool.acquire() as conn:
                    await conn.execute(
                        """
                        INSERT INTO collaborative_documents (room_id, state, updated_at)
                        VALUES ($1, $2, NOW())
                        ON CONFLICT (room_id)
                        DO UPDATE SET state = $2, updated_at = NOW();
                        """,
                        room, json.dumps(state)
                    )
    return {"status": "ok"}
```

---

## 5. Pre-populating a Room from Database

When a new room is opened, your backend can issue an authenticated client pass or write initial registers so clients receive existing PostgreSQL data immediately upon connecting:

```js
import { Syncwell } from "syncwell-client";

// One-off bootstrap client
const sw = new Syncwell(SYNCWELL_URL, "room-123", { token: bootstrapPass });
await sw.ready;

// Pre-fill fields from database
sw.batch(() => {
  sw.set("title", dbRecord.title);
  sw.set("content", dbRecord.content);
});
sw.close();
```
