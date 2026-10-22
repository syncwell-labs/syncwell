# Restore drill

This is the operator-facing restore drill for Syncwell.

The goal is to prove four things before relying on a backup strategy:

- a backup is complete and checksummed
- a corrupted backup is rejected instead of overwriting good data
- the data directory can be lost and rebuilt from the backup
- the restored service serves the recovered document to a fresh client

## Drill steps

1. Start Syncwell with persistence enabled and a persistent data directory.

   ```bash
   syncwell -data /var/lib/syncwell -secret "$SYNCWELL_SECRET"
   ```

2. Create a room, make a few edits, and confirm the state is visible in the
   browser demo. Stop the service.

3. Take an offline backup. This flushes and fsyncs every room log, copies them,
   and writes a `MANIFEST.json` with each file's size and SHA-256.

   ```bash
   syncwell -data /var/lib/syncwell -backup /backup/2026-10-20
   syncwell -verify-backup /backup/2026-10-20      # prints "backup ok: N files..."
   ```

4. (Recommended) Prove the backup is trustworthy: copy it, flip a byte in one
   `.log`, and confirm `-verify-backup` fails. `-restore` refuses the same copy.

5. Restore into a fresh host or container, or over the emptied data directory.

   ```bash
   syncwell -restore /backup/2026-10-20 -data /var/lib/syncwell
   ```

6. Start the same or newer Syncwell binary against the restored directory.

7. Verify:

   - `GET /healthz` returns `ok`
   - `GET /stats` or `GET /metrics` works with the admin token
   - the browser demo shows the recovered room state

## Automation

`tests/e2e/restore_drill.mjs` runs the whole loop in CI:

- boot the server, write a known room state, hard-kill it
- back up with `-backup`, verify with `-verify-backup`
- corrupt a copy and assert verification and restore both fail
- delete the data directory entirely
- restore with `-restore`, restart the server, and assert a fresh client sees the
  original document

Run it locally with:

```bash
make restore-drill
```

It is also part of `make all`, so regressions in the backup path fail CI.
