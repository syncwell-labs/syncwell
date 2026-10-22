# Compatibility policy

The v1 wire protocol was **frozen on 2026-10-20**. The full, normative policy is
[`docs/protocol/PROTOCOL_COMPATIBILITY.md`](https://github.com/Nil-Vaghani/syncwell/blob/main/docs/protocol/PROTOCOL_COMPATIBILITY.md).
This page summarizes the rules.

## Version negotiation

- `"protocol": 1` is the stable contract; absent or `0` means v1.
- Unsupported non-zero versions are rejected, never guessed or silently
  downgraded.
- The JavaScript and Python clients declare v1.

## Backward-compatible changes

Allowed in 0.x releases:

- optional fields, ignored by older readers;
- new server-to-client frame types, ignored by older clients;
- new HTTP endpoints, metrics, flags, env vars, and webhook event types;
- new token formats while all old formats keep working;
- loosening limits.

## Breaking changes

Require protocol v2 and a major release:

- removing/renaming fields or changing their meaning;
- changing merge order, tie-break, tombstones, epochs, or delta semantics;
- requiring a new client-to-server frame for baseline operation;
- requiring auth where it was optional;
- rejecting values that v1 accepted;
- changing webhook delivery semantics without an explicit versioned opt-in.

## Deprecation window

At least **one minor release and six months**, then removal only in a major
release. Security fixes may shorten the window; prefer limits/rejections over
wire breaks.

## Compatibility floor

Full snapshot + `merge` always works. Delta sync (`?delta=1`) is additive and
must fall back to snapshot/merge when unsupported or stale.

## Enforcement

Protocol version and unknown-frame contract tests live in
`internal/server/protocol_contract_test.go`. Wire changes must update the normative spec,
this policy, and tests in one pull request.
