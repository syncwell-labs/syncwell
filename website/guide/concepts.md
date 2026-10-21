# Core concepts

## Room

A room is one collaborative document. Each room is owned by a single server
goroutine; all mutations arrive as messages, so the room loop needs no locks.
Rooms are created on first join and evicted after an idle timeout (persisted data
is not deleted).

## Register and logical clock

A document key maps to a **register**:

```json
{
  "v": "Hello",
  "t": { "c": 8, "n": "client-a" },
  "d": false
}
```

- `v` is the value (any JSON value).
- `t.c` is a Lamport counter; `t.n` is the authoring node id.
- `d: true` is a tombstone (delete).

Merge order is `(counter, node)`, with a deterministic JSON tie-break for equal
timestamps. The client authors its own timestamp, which is what makes offline
editing possible. The server merges and fans out; it is not a sequencer.

## CRDT laws

The map merge is commutative, associative, and idempotent:

```text
merge(a, b) == merge(b, a)
merge(merge(a, b), c) == merge(a, merge(b, c))
merge(a, a) == a
```

Duplicate or reordered delivery therefore cannot corrupt state. The Go core and
the JavaScript SDK run the same rule and share conformance fixtures for JSON
canonicalization.

## Tombstones and GC

Deletes leave tombstones so a delete can beat a concurrent stale write. The
server tracks a causal low-water mark from connected clients and periodically
collects tombstones below it. Long-offline clients are protected by an epoch
check: if the room was compacted past their state, they resnapshot.

## Sequence text

`SequenceText` stores one character per register. Each character has a
fractional **rank**; an insert between two characters gets a rank between their
ranks, so concurrent typing at the same position interleaves deterministically
instead of colliding on an array index. Deleted characters are tombstones, which
keeps undo/redo and delayed offline edits causally safe.

## Awareness

Presence is ephemeral: users, cursor indices, and selections travel as
`presence` frames, are never persisted, and are not replayed on join. Heartbeats
let peers prune stale tabs even when a socket vanishes without a close frame.
