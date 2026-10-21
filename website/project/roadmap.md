# Roadmap

Each phase is independently shippable. A milestone is done only when its
acceptance criteria are proven in CI; owner-only publishing steps are tracked
separately.

| Phase | Theme | State |
| --- | --- | --- |
| 0 | Guardrails: coverage, fuzzing, security scanners | ✅ |
| 1 | Durability, backup/restore, crash safety | ✅ |
| 2 | Two-node full-mesh relay, anti-entropy | ✅ |
| 3 | SDK packaging, releases, TLS, deploy templates | ✅ code; tags owner-only |
| 4 | Safe binds, OIDC, admin hardening, threat model | ✅ |
| 5 | Metrics, tracing, SLOs, load/soak harness | ✅ |
| 6 | Text, awareness, undo, adapters, delta, admin, webhooks, Python | ✅ |
| 7 | Docs site, hosted demo, v1 freeze, launch | 🟡 local work complete; owner deploy |

## Milestones

| Milestone | State |
| --- | --- |
| M1 — Trustworthy build | ✅ |
| M2 — Installable | 🟡 code complete; release tags/docs hosting owner-only |
| M3 — Scalable | ✅ full-mesh relay; Redis/NATS remains stretch |
| M4 — Secure and observable | ✅ |
| M5 — Feature-complete | ✅ |
| M6 — Launched | 🟡 owner approval required |

## What is deliberately not done

- Redis/NATS transport and automatic node re-homing.
- Multi-region failover.
- A full block-based rich-text model; marks are per character.
- A hosted SaaS. Syncwell is self-hosted software.

The canonical board, release gates, and owner-only items are in
[`docs/MILESTONES.md`](https://github.com/Nil-Vaghani/syncwell/blob/main/docs/MILESTONES.md).
