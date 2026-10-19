# Restore drill

This is the operator-facing restore drill for Syncwell.

The goal is to prove three things before relying on a backup strategy:

- the service can restart cleanly
- the room snapshot and op-log can be restored
- the restored service still serves `/healthz`, `/stats`, and the browser demo

## Drill steps

1. Start Syncwell with persistence enabled and a persistent data directory.

   ```bash
   syncwell -data /var/lib/syncwell -secret "$SYNCWELL_SECRET"
   ```

2. Create a room, make a few edits, and confirm the state is visible in the
   browser demo.

3. Stop the service cleanly.

4. Snapshot or back up the data directory.

5. Restore the data directory into a fresh host or fresh container.

6. Start the same or newer Syncwell binary against the restored directory.

7. Verify:

   - `GET /healthz` returns `ok`
   - `GET /stats` or `GET /metrics` works with the admin token
   - the browser demo shows the recovered room state

## Automation hook

The repo includes a small Node-based restore-drill harness in
`test/restore_drill.mjs`. It is designed to be run against a built Syncwell
binary and will:

- boot the server with a temporary data directory
- write a known room state
- restart the server
- confirm the room still converges after restart

Run it in CI or locally after the Go binary is available.

