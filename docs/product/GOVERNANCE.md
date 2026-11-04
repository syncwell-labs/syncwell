# Governance

Syncwell is a small MIT-licensed project. Governance is intentionally lightweight:
clear ownership, public decisions, and a compatibility promise. The goal is to
keep the engine small and correct rather than maximize contribution volume.

## Roles

| Role | Responsibilities |
| --- | --- |
| Maintainer | Merges changes, cuts releases, stewards security and compatibility |
| Reviewer | Reviews protocol, CRDT, storage, and security changes; may be a maintainer |
| Contributor | Files issues, proposes designs, and opens pull requests |

The current maintainer team is Syncwell Labs. The list of people with merge or
release rights is the repository's collaborator list; there is no separate
private council.

## Decision making

- Routine fixes and docs: lazy consensus in the pull request.
- Wire protocol, merge semantics, persistence format, auth, and public API:
  a written proposal in an issue or `docs/architecture/DESIGN.md`, maintainer approval, and
  tests before merge.
- A maintainer makes the final call when consensus is not reached. The reasoning
  is recorded in the issue or pull request.
- Security reports are handled privately under `SECURITY.md`; details are
  published after a fix is available.

## Compatibility and releases

- The wire protocol and compatibility rules are in
  [`PROTOCOL_COMPATIBILITY.md`](../protocol/PROTOCOL_COMPATIBILITY.md).
- Releases follow semantic versioning for the server and SDK, with the protocol
  version tracked separately.
- There is no fixed release cadence. Security fixes and regressions are released
  as needed; feature releases are batched to keep the changelog readable.
- A release is not announced until CI, container smoke tests, and the restore
  drill are green.

## Contribution standards

- `make all` and `make test-python` must pass.
- CRDT or protocol changes need Go and JavaScript tests plus a compatibility
  review.
- New runtime dependencies need a written justification; the Go core is kept
  dependency-light by design.
- Documentation and examples are part of the change, not a follow-up.

## Roadmap

Milestones are tracked in [`MILESTONES.md`](./MILESTONES.md) and the
[`ROADMAP.md`](./ROADMAP.md). A milestone changes state only when its acceptance
criteria are demonstrably met. Owner-only steps (publishing, hosted infrastructure,
release tags) are listed explicitly rather than implied as complete.

## Code of conduct

Participation is covered by [`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md).
Security issues follow [`SECURITY.md`](../../SECURITY.md).
