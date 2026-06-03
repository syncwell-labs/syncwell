# Contributing to Syncwell

Thanks for your interest! Syncwell aims to be a small, correct, well-documented
real-time collaboration engine. Contributions that keep it that way are welcome.

## Getting started

```bash
git clone <your fork>
cd syncwell
make all      # vet + unit/race tests + JS tests + e2e + persistence + GC + build
make run      # http://localhost:8080  (and /kanban.html)
```

Requirements: Go 1.23+ and Node 18+ (for the JS tests and e2e harnesses).

## Project layout

| path | what |
| --- | --- |
| `internal/crdt` | the CRDT core + property/chaos tests — **the heart** |
| `internal/server` | room hub, websocket pumps, auth |
| `internal/store` | persistence (op-log + compaction) |
| `cmd/syncwell` | the single-binary entrypoint |
| `sdk/` | zero-dep browser/Node client + its CRDT mirror |
| `demo/` | runnable demos (text + kanban) |
| `test/` | Node e2e harnesses (offline, persistence, GC, bench) |
| `docs/` | PROTOCOL, ROADMAP, BENCHMARKS, BLOG |

## Ground rules

1. **Correctness is the product.** Any change to merge/ordering/GC must come with
   a test. The CRDT laws (commutative, associative, idempotent) are non-negotiable
   — see `internal/crdt/*_test.go` and the chaos test.
2. **Keep the Go core dependency-free.** The only third-party dep is the
   websocket library. Don't add more without discussion.
3. **The Go and JS CRDTs must agree.** If you change one, change and test both.
4. **Document trade-offs.** New design decisions go in `DESIGN.md` with their
   cost stated, not just their benefit.
5. **`make all` must be green** before you open a PR. CI runs the same.

## Good first issues

- A sequence CRDT (RGA) for collaborative *text* (currently LWW-Map only).
- `fsync` durability mode for the store (power-loss safety).
- A Go client (the protocol is tiny and language-agnostic).
- Horizontal scaling: shard rooms across processes via a pub/sub backbone.

## Reporting bugs

Open an issue with: what you did, what you expected, what happened, and ideally a
seed or a minimal repro against one of the `test/*.mjs` harnesses.
