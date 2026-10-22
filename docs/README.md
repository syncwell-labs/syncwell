# Documentation index

Everything written down about Syncwell lives here, grouped by who needs it.
The published docs site (VitePress) lives in [`../website/`](../website/) and
links back into these files.

| Folder | What it holds | Start here |
| --- | --- | --- |
| [`architecture/`](architecture/) | Design rationale, CRDT internals, convergence notes | [`architecture/DESIGN.md`](architecture/DESIGN.md) |
| [`protocol/`](protocol/) | The frozen v1 wire protocol, compatibility policy, access control & epochs | [`protocol/PROTOCOL.md`](protocol/PROTOCOL.md) |
| [`operations/`](operations/) | Deploy, operate, observe, back up, release | [`operations/OPERATIONS.md`](operations/OPERATIONS.md) |
| [`product/`](product/) | Roadmap, milestones, governance, launch plan, benchmarks, working plan | [`product/ROADMAP.md`](product/ROADMAP.md) |
| [`standards/`](standards/) | UX and accessibility standard every surface must satisfy | [`standards/UX_STANDARD.md`](standards/UX_STANDARD.md) |
| [`audits/`](audits/) | Point-in-time audits and completion plans (historical, dated) | — |

## Quick map

- **Running it:** [`operations/DEPLOY.md`](operations/DEPLOY.md) ·
  [`operations/OPERATIONS.md`](operations/OPERATIONS.md) ·
  [`operations/SLO.md`](operations/SLO.md) ·
  [`operations/ALERTS.md`](operations/ALERTS.md)
- **Securing it:** [`operations/THREAT_MODEL.md`](operations/THREAT_MODEL.md) ·
  [`protocol/ACCESS_CONTROL_AND_EPOCHS.md`](protocol/ACCESS_CONTROL_AND_EPOCHS.md) ·
  [`operations/ADMIN_AND_WEBHOOKS.md`](operations/ADMIN_AND_WEBHOOKS.md)
- **Shipping it:** [`operations/RELEASE_CHECKLIST.md`](operations/RELEASE_CHECKLIST.md) ·
  [`product/GOVERNANCE.md`](product/GOVERNANCE.md) ·
  [`product/MILESTONES.md`](product/MILESTONES.md)
- **Changing the wire format:** [`protocol/PROTOCOL_COMPATIBILITY.md`](protocol/PROTOCOL_COMPATIBILITY.md)
- **Changing the UI:** [`standards/UX_STANDARD.md`](standards/UX_STANDARD.md)

## Conventions

- Filenames are `UPPER_SNAKE_CASE.md`; the folder tells you the audience.
- Audits are immutable once dated — write a new one instead of editing an old one.
- Anything a user must configure belongs in
  [`operations/DEPLOY.md`](operations/DEPLOY.md) **and**
  [`../.env.example`](../.env.example) **and** the website's configuration page,
  with identical defaults (enforced by [`standards/UX_STANDARD.md`](standards/UX_STANDARD.md)).
