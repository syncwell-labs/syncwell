# Tests

Every harness here runs against the **real** Go binary or the **real** docs
site — no mocks of the server. Unit tests live next to the code they cover
(`internal/crdt/*_test.go`, `clients/javascript/*.test.js`); this folder holds
the cross-process proofs.

| Path | Harness | What it proves |
| --- | --- | --- |
| [`e2e/offline_e2e.mjs`](e2e/offline_e2e.mjs) | offline → reconnect | edits made offline merge to the same state as the server |
| [`e2e/multi_client_e2e.mjs`](e2e/multi_client_e2e.mjs) | many clients | concurrent writers converge without lost updates |
| [`e2e/persist_e2e.mjs`](e2e/persist_e2e.mjs) | SIGKILL + restart | state survives a hard kill |
| [`e2e/restore_drill.mjs`](e2e/restore_drill.mjs) | backup → restore | a corrupt backup is rejected; a good one restores |
| [`e2e/gc_e2e.mjs`](e2e/gc_e2e.mjs) | tombstone churn | tombstones are actually collected |
| [`e2e/container_smoke.mjs`](e2e/container_smoke.mjs) | Docker image | `/healthz`, `/`, and `/sdk/*` are served by the built image |
| [`e2e/browser_kanban_e2e.mjs`](e2e/browser_kanban_e2e.mjs) | Firefox | the kanban demo works in a real browser |
| [`e2e/browser_chromium_e2e.mjs`](e2e/browser_chromium_e2e.mjs) | Brave/Chromium | WebSocket auth, two-tab collaboration, reconnect, console/network errors, mobile layout |
| [`e2e/browser_firefox_e2e.mjs`](e2e/browser_firefox_e2e.mjs) | Firefox | the same auth/collaboration/reconnect/log checks through WebDriver BiDi |
| [`a11y/demo_a11y.test.mjs`](a11y/demo_a11y.test.mjs) | DOM assertions | demos expose labels, live regions, and roles |
| [`a11y/axe_a11y.py`](a11y/axe_a11y.py) | axe-core | WCAG 2.1 A/AA: 0 violations on demos **and** the built docs site, light and dark |
| [`bench/bench.mjs`](bench/bench.mjs) | benchmark | latency/throughput; writes [`../docs/product/BENCHMARKS.md`](../docs/product/BENCHMARKS.md) |
| [`conformance/`](conformance/) | fixtures | shared tie-break cases for the Go ↔ JavaScript CRDTs |
| [`helpers.mjs`](helpers.mjs) | shared | probe helpers used by every harness |

## Running them

```bash
make all          # the full gate: fmt, vet, unit, coverage, JS, e2e, durability
make test-python  # Python client against the real server
make a11y         # axe-core over demos + built docs
make bench        # regenerate docs/product/BENCHMARKS.md
```

Harnesses expect `bin/syncwell` to exist (run `make build` first) and pick up
`SYNCWELL_BIN` to point at a different binary. `a11y/axe_a11y.py` needs
`chromedriver` plus Chrome or Chromium; `e2e/browser_kanban_e2e.mjs` needs
Firefox and `geckodriver`. `e2e/browser_chromium_e2e.mjs` prefers Brave when its
major version matches `chromedriver`, and otherwise falls back to Chromium (run
`make browser-e2e-chromium`). A Brave/chromedriver major mismatch is reported
explicitly; set `BROWSER_BIN` to choose a specific browser.
`make browser-e2e-firefox` uses `geckodriver` and WebDriver BiDi to capture
console and network events; set `FIREFOX_BIN`/`GECKODRIVER_BIN` to override.

Third-party assets live in [`vendor/`](vendor/) and are not part of the
repository's source.
