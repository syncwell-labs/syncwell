# Clients

Official client libraries for the Syncwell protocol. Both speak the same frozen
v1 wire format ([`../docs/protocol/PROTOCOL.md`](../docs/protocol/PROTOCOL.md))
and merge with the same LWW-timestamp rule, so a room can mix languages.

| Client | Language | Install | Docs |
| --- | --- | --- | --- |
| [`javascript/`](javascript/) | TypeScript / JavaScript (zero dependencies) | `npm install syncwell-client` | [`javascript/README.md`](javascript/README.md) |
| [`python/`](python/) | Python 3.10+ (standard library only) | copy `python/syncwell_client.py` | [`python/README.md`](python/README.md) |

The browser/Node SDK is served by the engine itself at `/sdk/syncwell.js`, so a
self-hosted deployment needs no package install at all.

## Keeping clients in sync

The CRDT semantics are mirrored across implementations. A change to merge
ordering, garbage collection, or the wire format must land in the Go core
(`../internal/crdt`), the JavaScript client, and the Python client together —
see rule 3 in [`../CONTRIBUTING.md`](../CONTRIBUTING.md) and the frozen
compatibility policy
([`../docs/protocol/PROTOCOL_COMPATIBILITY.md`](../docs/protocol/PROTOCOL_COMPATIBILITY.md)).
