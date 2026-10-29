# ADR-001: Shared cluster transport and room ownership

- **Status:** Codex technical review recorded; implementation in progress; production validation pending
- **Date:** 2026-10-28
- **Decision owners:** Syncwell maintainers

## Context

The current cluster relay is bounded HTTP fan-out. It is useful for small,
static peer sets, but each node stores its own room state and full-mesh traffic
grows with the number of peers. It does not provide durable shared history,
exclusive room ownership, or safe re-homing when a node dies. A redirect or
header alone cannot route a browser WebSocket client reliably after a failed
handshake.

Re-homing must ensure that a replacement owner reconstructs acknowledged room
writes before accepting new clients. The design must retain the current relay
as a rollback mode and must not expose room IDs as metric labels.

## Decision proposed

Add an opt-in Redis Streams cluster mode while keeping `relay` as the default.
Redis is an external dependency for this mode only; single-node and relay
deployments keep operating without it.

- Use a SHA-256 room hash in Redis keys and streams. Never put a raw room ID in
  a Redis key, metric label, or routine log field.
- Store ordered room events in `sw:{roomhash}:events`. Maintain a compacted
  CRDT snapshot and its exact stream offset for fast recovery. Do not trim on
  each `XADD`: commit the snapshot and offset first, then trim only through the
  committed watermark (`XTRIM MINID ~ <snapshotOffset>`). This prevents a
  bounded-length trim from deleting events newer than the recoverable snapshot.
- Claim `sw:{roomhash}:owner` with `SET NX PX 15000`; renew every 5 seconds.
  A node that cannot renew stops accepting new writes for that room. It must
  not continue as an owner after its lease expires.
- Use a fencing epoch per room. Lease acquisition is an atomic Redis script
  that refuses an existing owner, increments
  `sw:{roomhash}:fence`, and stores `<node-id>:<process-nonce>:<epoch>` with the
  lease TTL. Renewal compares the complete owner value before extending TTL;
  release compares it before deleting. A restarted process gets a new nonce
  and epoch even when it reuses the same node ID.
- Commit every mutation through an atomic Redis script that checks the current
  lease value and fencing epoch before appending the event. A stale owner may
  not append after another owner takes the lease. A mutation is acknowledged
  only after its event append succeeds; Redis loss while the lease appears
  valid therefore fails writes closed instead of acknowledging local-only
  state.
- Re-home only after a new owner wins the lease, loads the latest snapshot,
  replays stream entries after the snapshot offset, and then marks the room
  ready. Clients reconnect to the new owner.
- Add `GET /route?room=...` and opt-in SDK `followOwner`. Non-SDK clients may
  receive HTTP 421 with `X-Syncwell-Owner` and `Location`, but browser SDK
  routing must use the explicit route endpoint.
- Require Redis AOF with `appendfsync everysec`; document a maximum 1 second
  persistence RPO and a target RTO bounded by lease expiry plus replay time.
- Redis failover must fence the old primary before the promoted primary accepts
  ownership or writes. The supported topology must prevent two writable
  primaries; if the selected Redis service cannot guarantee that, this lease
  design is insufficient and must be revised before implementation.
- The fencing guarantee is conditional on Redis service topology: this design
  does not make asynchronous Redis failover linearizable. Production support
  requires an infrastructure/provider guarantee that the former primary cannot
  accept writes after promotion. If continuity or fencing is uncertain, Redis
  mode must fail closed and must not be described as safe for re-homing.
- Store a snapshot and its exact last-applied stream ID atomically before
  trimming older events. Recovery uses `XRANGE` after that ID; consumer-group
  delivery acknowledgements are not a substitute for replaying the ordered
  stream. If the required range was trimmed or is missing, keep the room
  unavailable and require snapshot/operator recovery rather than serving
  partial state.
- Keep `-cluster-mode=relay` as the default and rollback path. Redis URLs and
  credentials must be redacted from logs and traces.

## Failure semantics

1. Redis unavailable: an owner may serve already accepted local state only
   until its lease deadline, but it cannot acknowledge writes without a
   successful fenced stream append. After lease loss, it stops accepting
   writes; no second owner may be elected based only on local reachability.
2. Owner process death: after lease expiry another node claims ownership,
   replays durable state, and advertises itself. Clients retry through the
   route endpoint.
3. Redis failover: the old primary must be fenced before promotion completes.
   AOF bounds local persistence loss, but does not by itself prove replication
   of an acknowledged stream append. Recovery tests must measure actual lost
   writes and duration; if the provider permits two writable primaries, the
   design fails review.
4. Stream trimming: snapshots must be committed with their exact stream offset
   before older events are trimmed. A missing offset range forces snapshot
   recovery or operator intervention; it must not silently serve partial state.

## Security and operations

- Redis requires TLS and authentication outside local development.
- Apply least-privilege ACLs to the key prefix and commands used by Syncwell.
- The Redis user needs `GET`, `SET`, `DEL`, `INCR`, `PEXPIRE`, `XADD`,
  `XRANGE`, `XTRIM`, `EVAL`, `EVALSHA`, and `CONFIG GET` for the append-only
  startup check. Scope key commands to `sw:*`; grant `CONFIG GET` only if the
  provider's ACL model supports restricting its arguments.
- Avoid logging Redis URLs, passwords, room IDs, or room contents.
- Export bounded metrics for lease state, replay duration, stream errors, and
  re-homing outcomes. Peer/node labels must come from configured node IDs.
- Document backup/restore for Redis and the existing encrypted local backups;
  these protect different state layers.

## Codex technical review (2026-10-29)

Reviewed at the user's request. The proposal is suitable as the basis for an
opt-in implementation prototype, subject to the safety constraints above. The
review specifically required snapshot-watermark trimming instead of
`MAXLEN`-on-every-write, explicit limits on guarantees during asynchronous
Redis failover, removal of consumer-group delivery as a recovery requirement
(ordered `XRANGE` replay is sufficient), and ensuring Redis event payloads do
not contain raw room IDs. These are design review findings, not maintainer or
provider approval. Production readiness remains pending maintainer acceptance
and successful provider failover validation.

Implementation now includes opt-in runtime configuration, fenced room writes
and snapshots, replay before room admission, lease-loss shutdown, route lookup,
and SDK opt-in owner following. Unit tests now use an injectable ticker and a
fake Redis room-store interface to exercise lease renewal/expiry and exact
multi-page replay offsets; Redis integration tests exercise the production Lua
fencing and snapshot scripts. A local Redis 7.4 drill starts three server
processes, SIGKILLs the owner during acknowledged writes, verifies re-homing,
then restarts all three processes in relay mode and verifies event convergence.
GitHub Actions has the same drill wired in, but its run is pending because the
workspace changes have not been pushed. Provider failover fencing/RPO evidence
and maintainer acceptance remain open. Redis mode refuses startup unless AOF
is enabled with `appendfsync everysec`.

## Required validation before implementation is accepted

- Review this ADR and record approval or requested changes here.
- [x] Unit-test lease renewal/expiry and replay pagination with an injectable
  clock and fake Redis room-store interface; production Redis Lua fencing and
  snapshot-offset behavior are covered by Redis integration tests.
- [x] Local Redis 7.4 integration drill with three server processes, owner
  SIGKILL during writes, acknowledged-write replay, and all three restarted in
  relay mode with cross-node convergence.
- [ ] Confirm the same integration drill passes in GitHub Actions.
- Kill the owner with SIGKILL during writes; the replacement must serve within
  lease TTL plus replay time, with no acknowledged-write loss beyond the
  documented 1 second RPO.
- Verify that stale owners cannot write after losing their lease.
- Exercise Redis primary failover and prove the old primary is infrastructure-
  fenced before the promoted primary grants leases or accepts appends; reject
  topologies where this cannot be demonstrated.
- Verify metrics and logs contain no raw room IDs or credentials.
- [x] Run a rollback test by restarting all three nodes in relay mode and
  verifying a write converges to a different peer.

## Consequences

This adds operational infrastructure and a second consistency mode. It
increases deployment complexity, but makes ownership and re-homing explicit
and testable. The current relay remains the supported multi-node mode until
maintainers accept the implementation and provider failover validation passes.
The Redis prototype remains opt-in and must not claim production re-homing
guarantees meanwhile.

## Alternatives considered

- **Keep relay only:** simplest and safest for small static clusters, but no
  ownership, no shared durable history, and full-mesh scaling remains.
- **Use Redis Pub/Sub only:** low latency, but transient messages do not provide
  replay or owner recovery after a process outage.
- **Use a relational database as the event bus:** provides persistence but
  introduces polling/locking behavior and does not simplify ownership leases.
- **Implement a custom consensus protocol:** rejected due to the correctness
  and maintenance cost.
