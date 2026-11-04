# Governance

Syncwell is MIT-licensed with a small maintainer-led governance model.

- **Maintainer:** merges, releases, security, and compatibility.
- **Reviewers:** protocol, CRDT, storage, and security review.
- **Contributors:** issues, proposals, documentation, and pull requests.

Routine changes use lazy consensus in the pull request. Protocol, merge,
persistence, auth, and public API changes need a written issue/design, maintainer
approval, and tests before merge. A maintainer makes the final call when
consensus fails, and records the reasoning publicly.

## Compatibility promise

The v1 wire protocol was frozen on 2026-10-20. Additive changes are allowed;
breaking changes require protocol v2 and a major release. Deprecations last at
least one minor release and six months. See
[Compatibility policy](/reference/compatibility).

## Releases

There is no fixed cadence. Security fixes and regressions ship as needed; feature
releases are batched. No release is announced before CI, container smoke tests,
and the restore drill pass.

## Quality bar

- `make all`, `make test-python`, and the docs build must pass.
- CRDT/protocol changes need Go and JavaScript tests.
- New runtime dependencies need written justification.
- Documentation and examples are part of the change.

The canonical policy is
[`docs/product/GOVERNANCE.md`](https://github.com/syncwell-labs/syncwell/blob/main/docs/product/GOVERNANCE.md).
Participation follows the
[Code of Conduct](https://github.com/syncwell-labs/syncwell/blob/main/CODE_OF_CONDUCT.md);
vulnerabilities follow
[SECURITY.md](https://github.com/syncwell-labs/syncwell/blob/main/SECURITY.md).
