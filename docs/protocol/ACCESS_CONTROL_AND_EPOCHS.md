# Access control and epoch design

This document records the access-control and epoch model now used by the
server, along with the remaining operational edges around room secret rotation
and GC policy.

## Goals

- Allow least-privilege room access instead of one room-wide bearer token.
- Make token rotation and revocation explicit.
- Prevent tombstone GC from allowing stale replicas to resurrect deleted data.
- Keep the current simple deployment story intact for trusted demos.

## Principal-aware capabilities

Each authenticated subject carries explicit capabilities for a room:

- `read`
- `write`
- `presence`
- `admin`

A token should describe:

- subject identity
- room id
- capability set
- expiry
- token id (`jti`)
- optional issued-at / not-before timestamps
- optional token version

Recommended defaults:

- `read` is enough to open a room in view-only mode.
- `write` is required for `set`, `delete`, and `merge`.
- `presence` is optional and can be omitted for bots or background workers.
- `admin` is reserved for operations like `/stats`, room inspection, and
  maintenance endpoints.

The server treats a **missing or malformed** capability claim as a hard deny
for OIDC/JWT tokens: a token whose caps claim is absent, empty, or unparseable
is rejected rather than silently granted read/write access. For HMAC room
tokens minted by the Syncwell token tooling, an empty capability list defaults
to the standard room set (`read,write,presence`) for backward compatibility —
mint tokens with explicit capabilities whenever least-privilege matters.

## Token rotation and revocation

Stateless room tokens are still fine for trusted deployments, and the current
implementation keeps them revocable without a token database:

- Short-lived tokens should be the default, not long-lived ones.
- The server accepts a revocation set of `jti` values.
- Rotation should be done by minting new tokens with a new secret or key id and
  expiring the old ones out of band.
- Revocation should be checked before room admission and before privileged
  maintenance actions.

Operationally, the current model is:

1. Mint a token from a trusted backend.
2. Deliver it to the client over an authenticated channel.
3. Accept it only until expiry or explicit revocation.
4. Prefer short TTLs for writable rooms and admin actions.

## Epoch semantics for tombstone GC

Tombstone garbage collection is safe only if the server can distinguish between
replicas that are merely quiet and replicas that are permanently gone.

The room epoch/lease model is now enforced on stale reconnects:

- Each room keeps a monotonically increasing epoch.
- A connected replica tracks the current epoch while it is active.
- The room advances GC only when connected clients have observed the relevant
  tombstone frontier.
- A reconnect from a stale epoch is rejected until the client resnaps or
  rejoins with the current epoch.

This keeps the good part of GC, but removes the ambiguity that allows a stale
offline client to resurrect deleted data after collection.

## Suggested wire changes

The protocol already carries most of these fields:

- token claims: `sub`, `room`, `caps`, `exp`, `jti`, `iat`, `nbf`
- room state: `epoch`
- client messages: `epoch` or `lease`
- error frames: explicit auth and stale-epoch failures

That gives the SDK enough information to detect stale reconnects and to present
clear errors instead of silently retrying forever.

## Operational impact

Once implemented, the operations guide should document:

- how to mint read-only versus writable tokens
- how to revoke a token by `jti`
- how to rotate the room secret
- how to force a room epoch bump during a maintenance window
- how to explain a stale-epoch reconnect to an operator
