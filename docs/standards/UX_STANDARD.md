# UX Standard — what "user-friendly" means here

This document defines the word. Any change that claims to improve usability must
map to a criterion below and pass its verification. Anything that does not is
opinion, not a UX fix.

## Personas

| Persona | Goal | First surface |
| --- | --- | --- |
| **Evaluator** — a developer with 5 minutes | See multiplayer sync working | README quickstart → demo pages |
| **Integrator** — embedding the SDK | Copy a snippet that runs | `clients/javascript/`, `website/guide/getting-started.md` |
| **Self-hoster** — running it for real | Configure, deploy, operate | CLI `--help`, `.env.example`, `docs/operations/DEPLOY.md` |

The Evaluator is the tie-breaker: when personas conflict, the 5-minute path wins.

## Criteria

Each criterion is pass/fail and has a concrete verification. "Mostly" is fail.

### 1. Time-to-value

> A first-time visitor sees live sync in **≤ 5 minutes, ≤ 3 commands**, without
> hand-editing any file.

**Verify:** follow the README quickstart verbatim on a clean machine with Go
installed. Count commands and minutes. The loopback path
(`go run ./cmd/syncwell -addr 127.0.0.1:8080`) is the canonical route.

### 2. Zero-config start

> The documented default start command (`make run`, `./syncwell`) serves a
> working demo, **or** prints the exact next command to make it work.

**Verify:** run `make run`, open `http://localhost:8080`. The demo must load and
sync in a second tab with no token, no `.env` editing, and no 401.

### 3. Every error names the next action

> An error message says what to do next, and names **both** the flag and its
> env var when either satisfies it.

**Verify:** `syncwell -mint x` (no secret) → message mentions `-secret or
SYNCWELL_SECRET`. Same for `-data`, `-tls-*`, `-oidc-*`, clustering validation.
A raw code with no remedy (`error: SERVER_ERROR` in the demo UI) is a fail.

### 4. No unexplained jargon on first contact

> The first appearance of a term (CRDT, room token, room secret, epoch,
> tombstone, state vector, LWW) on any surface carries a one-line gloss or is
> replaced with plain words.

**Verify:** read the README quickstart, `website/index.md` hero,
`website/guide/getting-started.md`, and the demo pages as the Evaluator would.
Underline every technical term at first use; each must be explained in place.

### 5. Docs match code

> No documented flag, env var, or default may differ from `cmd/syncwell/main.go`.

**Verify:** for every row in `website/self-hosting/configuration.md`,
`docs/operations/DEPLOY.md` env tables, and `.env.example`, the symbol exists in code and
the stated default equals the flag default. Undocumented claims are fails.

### 6. Every snippet runs

> Copy-paste of any code block completes without `ReferenceError`, 404, or a
> missing step.

**Verify:** paste each snippet from `README.md`, `clients/javascript/README.md`, and
`website/guide/*.md` into a clean context and run it. Unpublished artifacts
(npm package, GHCR image) must be labeled as such before the command.

### 7. One word per concept

> The same concept uses the same human-readable label on every surface.

**Verify:** search the demo pages and docs for each concept. Current canonical
words: **Share** (invite), **People here** (connected users), **Home** (index
demo), **room** (shared session), **token** (only in auth docs, never in demo UI).

### 8. Accessibility baseline

> Every interactive surface is keyboard-operable, announces state changes, and
> meets WCAG AA contrast in light and dark mode.

**Verify:** tab through each demo page — no trapped/missing focus, dialogs trap
focus and close on Escape, `role="status"` announces connection changes, no
label points at an `aria-hidden` target, no interactive element nested inside
`role="button"`.

### 9. Resilience is visible

> Disconnect and reconnect are perceivable, not silent.

**Verify:** kill the server while a demo tab is open → pill shows offline
(plural, plain language); restart → a "back live" confirmation appears. No raw
protocol codes.

## Design language (v1 — "editorial ink & paper")

Every user-facing surface (demo pages, admin dashboard, examples, website)
uses one visual system. New UI must follow it; do not reintroduce the old
template look.

| Token | Light | Dark |
| --- | --- | --- |
| `--bg` (paper) | `#f2eee6` | `#17140f` |
| `--panel` | `#fbf8f1` | `#201c15` |
| `--panel-muted` | `#e9e2d4` | `#2a241b` |
| `--text` (ink) | `#1d1a15` | `#ece5d5` |
| `--muted` | `#6a6154` | `#a09684` |
| `--accent` (vermilion) | `#a03a22` | `#d9825e` |
| `--border` (hairline) | `rgba(29,26,21,.16)` | `rgba(236,229,213,.15)` |
| `--ok` / `--warn` / `--err` | `#2f6a3d` / `#8a5a10` / `#9b2c2c` | `#7fb88a` / `#d3a556` / `#d97b7b` |

Rules:

- **Type:** serif display stack for headings, kickers, section labels, and
  taglines (`"Iowan Old Style", "Palatino Linotype", Palatino, Charter, Georgia, serif`);
  system sans for body; `ui-monospace` for data, URLs, status.
- **No:** gradients (incl. gradient text), radial background glows, pill
  badges, 999px radii, big soft shadows, saturated blue (`#2f6bff`) — these
  are the "AI template" tells.
- **Shape:** hairline 1px borders; radius 4px controls, 6px panels.
- **Buttons:** primary = ink-filled, hover → vermilion; secondary = hairline.
- **Status:** mono text + small square dot (`#status` / `--ok|--warn|--err`),
  never a filled pill.
- **Structure:** uppercase letterspaced kicker → serif masthead → hairline
  rule → italic serif tagline; serif underlined nav links.
- **Focus:** `2px solid var(--accent)` outline, 2px offset (no glow rings).
- Weights of the same idea live in one place: `examples/demos/demo-common.css` and the
  inline `:root` blocks of each page (kept identical on purpose — no build).

Verification: screenshot every page in light **and** dark after any style
change and compare against this table.

## Definition of done

A change passes this standard only when:

- [ ] It touches at least one criterion above and its verification passes.
- [ ] New/changed flags and env vars are documented in `.env.example`, `docs/operations/DEPLOY.md`, and `website/self-hosting/configuration.md` — with identical defaults.
- [ ] New UI copy uses the canonical words in criterion 7.
- [ ] New jargon in user-visible text carries a gloss at first use.
- [ ] `make fmt-check vet test test-js` passes; demo E2E (`?test=1` path) passes when demo files changed.
- [ ] `make a11y` reports 0 axe violations when a page, component, or docs theme file changed.

## Audit snapshot (2026-10-21, pre-fix)

| # | Criterion | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Time-to-value | Partial | Loopback path is 3 steps, but quickstart is buried after benchmarks (`README.md:64`) and the Go 1.23+ prerequisite only appears in `CONTRIBUTING.md:17`. |
| 2 | Zero-config start | Fail | `make run` omits `-addr 127.0.0.1:8080` (`Makefile:46`), triggers public-bind credential generation (`main.go:165-197`), and the demo then 401s (`server.go:353-358`). |
| 3 | Error names next action | Fail | `"-mint requires -secret"` (`main.go:112`) omits the env var; demo shows raw `error: <code>` (`examples/demos/demo-common.js:194-197`). |
| 4 | No unexplained jargon | Fail | "CRDT" at `README.md:13` before any definition; "room tokens" first used at `README.md:83` unexplained; hero copy lists "LWW map / tombstone GC" (`website/index.md:22-34`). |
| 5 | Docs match code | Fail | `-max-clients`/`-max-rooms` documented "unlimited" but default 256/10000 (`configuration.md:40-41`, `DEPLOY.md:174-175`); phantom `SYNCWELL_BACKUP`, `SYNCWELL_MAX_VALUE_BYTES` env vars; `.env.example:43` anti-entropy default wrong. |
| 6 | Snippets run | Fail | `render()` undefined in every getting-started snippet (`getting-started.md:63`); `npm install syncwell-client` not published (`clients/javascript/README.md:8`); website Docker quickstart pulls an unpublished GHCR image. |
| 7 | One word per concept | Fail | "Map demo" nav label is actually the notes demo (`kanban.html:255`); peers labeled "People here" / "Collaborators" / "connected peers" across pages. |
| 8 | Accessibility baseline | Partial | Share dialog lacks a Tab focus trap (`demo-common.js:113-134`); `#preview` label points at `aria-hidden` target (`sequence.html:155-156`); kanban shortcut hint only in `aria-label`. |
| 9 | Resilience visible | Partial | Offline pill exists; no "back live" confirmation after reconnect (`demo-common.js:180-199`). |

## Re-audit (2026-10-21, post-fix)

| # | Criterion | Status | Verification run |
| --- | --- | --- | --- |
| 1 | Time-to-value | **Pass** | README opens with quickstart (`README.md:19`): one `go run` command, URLs listed, Go 1.23+ prerequisite stated. |
| 2 | Zero-config start | **Pass** | `make run` binds `127.0.0.1:8080` (`Makefile`); banner prints "no token needed"; demo verified live over HTTP. |
| 3 | Error names next action | **Pass** | Flag errors name env vars (`-mint requires -secret or SYNCWELL_SECRET`, `-data or SYNCWELL_DATA`, tls-pair, oidc-issuer); `Config.Validate()` does the same; demo shows title + raw code + hint. |
| 4 | No unexplained jargon | **Pass** | First diagram label is "conflict-free doc"; "CRDT laws" glossed; "room token" and "secret" defined at first use (README quickstart, getting-started); hero/features in plain language; clients/javascript/README CRDT glossed. |
| 5 | Docs match code | **Pass** | Scripted check: every env var in `-h` appears in `docs/operations/DEPLOY.md` (direct or shorthand row); configuration.md vars all validated against `-h`; defaults fixed (256/10000, no phantom vars, anti-entropy 30s). |
| 6 | Snippets run | **Pass** | `render`/`renderText`/`renderCursors` defined; npm-not-published caveated in clients/javascript/README, sdk.md, getting-started, persistence, text-and-awareness; website quickstart builds locally; `make docs-build` succeeds. |
| 7 | One word per concept | **Pass** | Canonical words enforced (Share / People here / Home / room / token); nav labels consistent ("Home", "Kanban board", "Shared document"); a11y test asserts "People here". |
| 8 | Accessibility baseline | **Pass** | Share-dialog Tab focus trap + background `aria-hidden` toggle; `#preview` label points at a visible `role="note"`; kanban hint restored; kanban card is `role="group"` (no interactive-inside-button); `node --test tests/a11y/demo_a11y.test.mjs` 3/3; `make a11y` (axe-core, WCAG 2.1 A/AA) reports **0 violations** across all demo surfaces and 10 built website pages in light **and** dark — code-block token colors remapped to ≥4.5:1 and the DocSearch button's visible text == its accessible name. |
| 9 | Resilience visible | **Pass** | Reconnect shows `back live — synced` (reverts after 4s); status pill visible on all three demo pages over HTTP. |

This table is updated as fixes land; criterion status must never be marked pass
without running its verification.
