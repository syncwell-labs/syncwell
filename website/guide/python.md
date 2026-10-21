# Python client

The dependency-free Python client proves the protocol is language-agnostic. It
uses only the standard library and shares the same LWW merge rule.

Requires Python 3.10+.

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

Offline edits are kept locally and merged on the next reconnect. Presence is
ephemeral:

```python
sw.presence({"cursor": {"index": 4}})
sw.on("presence", lambda event: print(event))
sw.on("leave", lambda event: print("left", event))
```

Run the real-server E2E test:

```bash
make test-python
# or
python3 -m unittest python/test_client_e2e.py -v
```

The test starts the Go server on `127.0.0.1:8131`, converges two Python clients,
disconnects one, edits offline, and verifies the reconnect merge.

Source and full API:
[`python/`](https://github.com/Nil-Vaghani/syncwell/tree/main/python).
