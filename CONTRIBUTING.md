# Contributing to Syncwell

Thanks for your interest! Syncwell aims to stay a small, correct, well-documented
real-time collaboration engine.

## Getting started

```bash
git clone https://github.com/<you>/syncwell
cd syncwell
make all            # vet + race/unit + coverage + JS + e2e + persistence + GC + build
make test-python    # dependency-free Python client against the real server
make docs-build     # build the VitePress docs site
make run            # http://localhost:8080, /kanban.html, /sequence.html
```

Requirements: Go 1.26+, Node 18+, and Python 3.10+ for the Python client.
Browser E2E additionally needs `firefox` and `geckodriver`.

`make all` does not install npm packages or need network access. Run
`make sdk-install` and `make docs-install` once when working on the SDK or docs.

## Project layout

| path | what |
| --- | --- |
| `internal/crdt` | CRDT core (LWW map + sequence) and property/chaos tests — **the heart** |
| `internal/server` | room hub, WebSocket pumps, auth, admin, webhooks, cluster relay |
| `internal/store` | append-only persistence, snapshots, compaction, backup/restore |
| `cmd/syncwell` | single-binary entrypoint |
| `clients/javascript/` | zero-dependency JS client: map, text, awareness, storage, types |
| `clients/python/` | dependency-free Python client and real-server E2E |
| `examples/` | React, Vue, Svelte, vanilla, and Node integrations |
| `examples/demos/` | state, kanban, and collaborative text demos |
| `tests/e2e/` | Node E2E: offline, multi-client, persistence, restore, GC, container |
| `tests/a11y/` | axe-core and DOM-level accessibility checks |
| `tests/bench/` | latency/throughput benchmark harness |
| `tests/conformance/` | shared cross-client conformance fixtures |
| `deploy/` | `proxies/`, `systemd/`, `monitoring/` deployment artifacts |
| `website/` | VitePress documentation site |
| `docs/` | `architecture/` `protocol/` `operations/` `product/` `standards/` `audits/` |

## Ground rules

1. **Correctness is the product.** Any change to merge, ordering, GC, or the wire
   protocol needs tests. The CRDT laws (commutative, associative, idempotent)
   are non-negotiable — see `internal/crdt/*_test.go` and the chaos test.
2. **Keep the Go core dependency-light.** The only runtime dependency is the
   WebSocket library. Justify any new dependency in the pull request.
3. **The Go and JavaScript CRDTs must agree.** Change and test both mirrors.
4. **No silent v1 breaks.** Read
   [`docs/protocol/PROTOCOL_COMPATIBILITY.md`](./docs/protocol/PROTOCOL_COMPATIBILITY.md) before
   touching a frame, merge rule, limit, or auth format.
5. **Document trade-offs.** New design decisions go in `docs/architecture/DESIGN.md` with their
   cost stated, not just their benefit.
6. **Green before review.** `make all`, `make test-python`, and `make docs-build`
   must pass. CI runs the same plus the container smoke and browser E2E.
7. **UI follows the UX standard.** Any change to a page, component, or doc
   snippet must satisfy `docs/standards/UX_STANDARD.md` — including its "editorial ink &
   paper" design language (serif display, hairline borders, no gradients or
   pill badges). Screenshot light **and** dark after visual changes, and run
   `make a11y` (axe-core must report 0 violations).

## Good first issues

- A Go client for the frozen v1 protocol.
- A shared protocol-conformance suite for the JavaScript, Python, and Go clients.
- More framework examples (Solid, Angular, htmx) under `examples/`.
- Sequence-text interleaving and awareness fan-out benchmarks.
- Docs improvements: deployment recipes, troubleshooting, accessibility.

Everything in this list is genuinely open; larger items should start with an
issue so the design can be agreed before implementation.

## Pull requests

Include the problem, the approach, trade-offs, and the commands you ran. Keep
unrelated refactors out of the same PR. Update docs and examples with the code.

- Bug reports: use the issue template with a minimal repro and, ideally, a seed
  for one of the `tests/e2e/*.mjs` harnesses.
- Security issues: use the private advisory flow in
  [`SECURITY.md`](SECURITY.md), not a public issue.
- Design/process: [`docs/product/GOVERNANCE.md`](./docs/product/GOVERNANCE.md) and
  [`docs/product/MILESTONES.md`](./docs/product/MILESTONES.md).

## Review and release

A maintainer reviews correctness, tests, compatibility, and operational impact.
Releases follow the gates in `docs/product/MILESTONES.md`; breaking protocol changes
require protocol v2 and a major release.
