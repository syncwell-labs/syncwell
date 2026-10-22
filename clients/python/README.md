# Python client

A dependency-free client for the Syncwell WebSocket protocol. It exists to prove
the server and CRDT semantics are language-agnostic: the Python client merges
against the same browser/Node clients with the same LWW timestamp rule.

Requires Python 3.10+ (standard library only).

```python
from syncwell_client import Syncwell

sw = Syncwell("ws://localhost:8080/ws", "my-room", client_id="py-worker")
sw.on("change", lambda live: print(live))
sw.connect()
assert sw.wait_ready(5)

sw.set("title", "Hello from Python")
sw.delete("obsolete")
print(sw.live())

sw.close()
```

Offline edits are kept locally and shipped on the next reconnect. `presence()`
sends ephemeral state; use `on("presence", ...)` and `on("leave", ...)` to track
peers.

Run the real-server end-to-end test (builds nothing; uses `bin/syncwell`):

```bash
make test-python
# or, after a `make build` (the test reuses bin/syncwell and builds nothing):
python3 -m unittest clients/python/test_client_e2e.py -v
```

The test starts the Go server on `127.0.0.1:8131`, connects two Python clients,
verifies a write converges, then disconnects one client, edits offline, and
reconnects to prove the changes merge.
