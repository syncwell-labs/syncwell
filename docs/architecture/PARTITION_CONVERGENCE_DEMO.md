# Partition convergence GIF

This is the visual proof for the public demo: one browser keeps editing while
partitioned, another browser continues online, then reconnect merges both CRDT
replicas until the boards match.

## Capture flow

1. Start Syncwell and open the same room in two windows:

   ```text
   http://localhost:8080/kanban.html?room=phase-2-gif
   ```

2. In the left window, click **Simulate partition**.

3. Add or move a card in the left window. The status should say
   `partitioned — editing locally…`.

4. Add or move a different card in the right window while it stays live.

5. In the left window, click **Reconnect + merge**.

6. Keep recording until both windows show the same cards and the status returns
   to `live` / `reconnected — merge sent`.

## What the GIF should prove

- Offline edits apply instantly because the browser owns a local CRDT replica.
- Online peers can keep editing during the partition.
- Reconnect uses the `merge` protocol frame to send the partitioned client's
  full state back to the server.
- Both windows converge without manual conflict resolution.

## Suggested export

Record a short `.mov`/`.webm`, then export a compact GIF:

```bash
ffmpeg -i partition-convergence.mov \
  -vf "fps=12,scale=960:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse" \
  docs/assets/partition-convergence.gif
```

Keep the GIF to 8-12 seconds and end on the converged board.
