# Protocol v1 compatibility policy

**Status:** frozen on 2026-10-20. This policy covers the WebSocket wire protocol
and the public HTTP endpoints used by clients and operators.

`protocol: 1` is the stable contract for Syncwell 0.x servers and the JavaScript
and Python clients. Additive changes may ship in 0.x releases; breaking changes
require a new protocol version and a major release.

## Version negotiation

- The WebSocket endpoint is `GET /ws?room=<id>`; frames are JSON text.
- Frames may carry `"protocol": 1`. An absent or `0` value means v1.
- A client or server that receives an unsupported non-zero protocol version must
  reject the connection/operation rather than guess. The current server closes
  with WebSocket policy violation (`1008`) and an `error` frame when possible.
- There is no silent downgrade from v2 to v1. A client must opt in explicitly to
  an older version it supports.
- The JavaScript SDK exports `protocolVersion === 1`. The Python client declares
  the same version.

## Allowed within v1 (backward compatible)

- Adding optional fields to existing frame or payload objects. Unknown fields
  must be ignored; they must not change the meaning of known fields.
- Adding new server-to-client frame types. Clients must ignore unknown types.
- Adding HTTP endpoints, response fields, webhook events, metrics, flags, and
  environment variables.
- Adding accepted token formats while continuing to accept every previously
  accepted v1 format.
- Loosening limits. Raising a published minimum or rejecting values that v1
  previously accepted is not allowed within v1.
- Performance, persistence-format, and internal implementation changes that do
  not change observable v1 semantics.

## Requires a new protocol version

- Removing, renaming, or changing the type/meaning of an existing field.
- Changing merge order, the Lamport ordering rule, tombstone semantics, the
  deterministic equal-timestamp tie-break, epoch behavior, or delta semantics.
- Requiring a new client-to-server frame for baseline operation. New client-to-
  server frames need capability discovery and must not break older servers.
- Changing auth from optional to required, or requiring a new token format.
- Making a previously accepted register, room ID, key, or message invalid.
- Changing webhook delivery from at-most-once without an explicit, versioned
  opt-in.

New client-to-server frame types should be gated on an additive server capability
(for example, a future optional `capabilities` field in `snapshot`/`delta`).
Until such discovery exists, a client must not send them.

## Deprecation

- Announce a deprecation in the changelog, docs, and release notes.
- Keep the deprecated behavior for at least **one minor release and six months**,
  whichever is longer.
- Remove it only in a major release that also increments the protocol version.
- Security fixes may shorten this window. When possible, mitigation is a limit or
  rejection rather than a wire-format break.

## Compatibility matrix

| Component | v1 support |
| --- | --- |
| Go server 0.3+ | v1, delta opt-in (`?delta=1`), full snapshot fallback |
| `syncwell-client` 0.3+ | v1, delta default with automatic fallback |
| Python client (`python/`) | v1, full state merge/snapshot |
| Older clients | Expected to work if they send v1 frames; not separately tested |

The full snapshot/`merge` exchange is the compatibility floor. Delta sync is
optional and must always fall back to snapshot/merge when unsupported or stale.

## Contract checks

- `internal/server/protocol_test.go` asserts the frozen version and unknown-frame
  rejection.
- JavaScript protocol tests assert the SDK's frame shape and merge behavior.
- `docs/PROTOCOL.md` remains the normative v1 field reference.
- Changes that touch wire semantics must update `docs/PROTOCOL.md`, this policy,
  and a conformance test in the same pull request.

## References

- [Wire protocol](./PROTOCOL.md)
- [Design decisions](./DESIGN.md)
- [Threat model](./THREAT_MODEL.md)
