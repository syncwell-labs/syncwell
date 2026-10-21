# Contributing

Thanks for helping. Syncwell aims to stay small, correct, and well documented.

## Set up

```bash
git clone https://github.com/<you>/syncwell
cd syncwell
make all            # Go race/unit, JS, e2e, persistence, GC, chaos, build
make test-python    # dependency-free Python client E2E
make docs-build     # VitePress
make run            # http://localhost:8080
```

Requirements: Go 1.23+, Node 18+, Python 3.10+ for the Python client.

## Where to start

| Path | What |
| --- | --- |
| `internal/crdt` | CRDT core, property and chaos tests |
| `internal/server` | Hub, rooms, WebSocket, auth, cluster relay |
| `internal/store` | Append-only persistence and compaction |
| `sdk/` | Zero-dependency JS client, text, awareness, storage |
| `demo/` | State, kanban, and sequence demos |
| `test/` | Node E2E, persistence, GC, chaos, bench harnesses |
| `python/` | Dependency-free Python client and E2E |
| `website/` | VitePress documentation site |
| `docs/` | Protocol, design, operations, roadmap, launch |

## Ground rules

1. **Correctness is the product.** Merge, ordering, GC, or protocol changes need
   tests. The CRDT laws are non-negotiable.
2. **Keep the Go core dependency-light.** Only the WebSocket library is a runtime
   dependency; justify anything new.
3. **Go and JS must agree.** Change and test both CRDT mirrors together.
4. **Document trade-offs.** Record design decisions and costs in `DESIGN.md`.
5. **Green before review.** `make all`, `make test-python`, and `make docs-build`
   must pass.
6. **No silent protocol breaks.** Follow the
   [compatibility policy](/reference/compatibility).

## Good first issues

- A Go client for the documented v1 protocol.
- A protocol conformance suite shared by JavaScript, Python, and Go clients.
- More framework examples (Solid, Angular, htmx).
- Benchmarks for `SequenceText` interleaving and awareness fan-out.
- Docs improvements: deployment recipes, troubleshooting, accessibility.

## Pull requests

Include the problem, the approach, trade-offs, and the commands you ran. Keep
unrelated refactors out of the same PR. For security issues, use the private
advisory flow, not a public issue.

## Review and merge

A maintainer reviews for correctness, tests, compatibility, and operational
impact. See [Governance](/project/governance) for decision-making and release
policy.
