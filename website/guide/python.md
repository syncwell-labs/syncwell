# Python client

The dependency-free Python client proves the protocol is language-agnostic. It
uses only the standard library and shares the same LWW merge rule.

Requires Python 3.10+.

When using room tokens, the Python SDK sends them in an
`Authorization: Bearer` handshake header by default. For legacy servers that
only accept query tokens, pass `allow_query_token=True` explicitly.
For Redis ownership deployments, pass `follow_owner=True` to resolve the
`/route` endpoint before connecting and after each explicit reconnect. It is
disabled by default.

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
# or, after a `make build` (the test reuses bin/syncwell and builds nothing):
python3 -m unittest clients/python/test_client_e2e.py -v
```

The test starts the Go server on `127.0.0.1:8131`, converges two Python clients,
disconnects one, edits offline, and verifies the reconnect merge.

Source and full API:
[`clients/python/`](https://github.com/syncwell-labs/syncwell/tree/main/clients/python).
