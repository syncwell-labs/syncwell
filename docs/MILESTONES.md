# Milestones

A milestone is **done** only when its acceptance criteria are met in CI and the
artifacts are available to a user. Owner-only release or infrastructure steps are
called out separately.

| Milestone | Scope | State | Evidence |
| --- | --- | --- | --- |
| **M1 — Trustworthy build** | Phases 0 + 1 | ✅ Complete | Coverage gate, fuzz/chaos, crash tests, backup/restore drill in `make all` |
| **M2 — Installable** | Phase 3 + docs site | 🟡 Code complete; release tags and docs hosting owner-only | SDK build/types, GoReleaser, GHCR, npm workflow, deploy templates, VitePress site |
| **M3 — Scalable** | Phase 2 | ✅ Complete for full-mesh HTTP relay | Two-node convergence and anti-entropy tests; Redis/NATS transport remains a stretch item |
| **M4 — Secure & observable** | Phases 4 + 5 | ✅ Complete | Safe binds, OIDC, threat model, metrics/traces, dashboard, SLOs, load harness |
| **M5 — Feature-complete** | Phase 6 | ✅ Complete | Text/awareness, undo, adapters, delta sync, examples, admin/webhooks, Python client |
| **M6 — Launched** | Phase 7 | 🟡 In progress; owner approval required | Docs build, protocol v1 freeze, governance; demo/status site, tagged deploy, npm/GHCR releases, and launch post are owner-only |

## Release gates

- [ ] All CI jobs green on the release commit.
- [ ] `make all`, `make test-python`, and `make container-smoke` pass locally or in CI.
- [ ] Container image builds and passes the smoke test.
- [ ] Restore drill passes on the release artifact.
- [ ] Protocol compatibility checks pass; no v1 break without a v2 plan.
- [ ] Release notes list breaking changes, migrations, and known issues.
- [ ] `npm publish` and GHCR push use provenance/attestations.
- [ ] Hosted demo is pinned to the release tag and `/healthz` + `/readyz` are green.

## Owner-only board

| Item | Owner action |
| --- | --- |
| Tag and publish server/SDK | Push `vX.Y.Z` and `sdk-vX.Y.Z`; verify release workflows |
| Publish npm package | Confirm `NPM_TOKEN`, provenance, and `syncwell-client@X.Y.Z` |
| Deploy docs site | Enable GitHub Pages with **GitHub Actions** source; run the Docs workflow |
| Deploy hosted demo/status | Deploy a released image, set `DEMO_URL`, add uptime check |
| Publish launch post | Replace the repo placeholder and publish on Show HN/dev.to |
| Refresh README metrics | Run the live-demo measurement; update the numbers table |

See [`LAUNCH.md`](./LAUNCH.md) for the step-by-step runbook.
