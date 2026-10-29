---
layout: home

hero:
  name: Syncwell
  text: Multiplayer without the backend tax
  tagline: Conflict-free shared state, collaborative text, live cursors, and offline sync — in a single self-hostable Go binary.
  image:
    src: /logo.svg
    alt: Syncwell
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: Self-host it
      link: /self-hosting/quickstart
    - theme: alt
      text: GitHub
      link: https://github.com/syncwell-labs/syncwell

features:
  - title: Shared state and text
    details: Key–value state for structured documents plus character-level collaborative text — concurrent edits merge automatically with no conflicts and no lost writes.
  - title: Offline-first
    details: Clients hold a local copy. Edits apply instantly, survive reloads, and merge on reconnect without a replay log.
  - title: Delta reconnect
    details: Returning clients download only the changes they missed instead of the whole document, so reconnects are fast.
  - title: Presence and awareness
    details: Ephemeral users, cursors, and selections with heartbeats and stale-peer pruning.
  - title: Durable and observable
    details: Append-only persistence, crash recovery, automatic cleanup of deleted data, Prometheus metrics, tracing, admin dashboard, and signed webhooks.
  - title: Language-agnostic
    details: A typed JavaScript SDK, a dependency-free Python client, and a small documented JSON/WebSocket protocol.
---

## Five-minute tour

Start the server on loopback (no credentials needed):

```bash
go run ./cmd/syncwell -addr 127.0.0.1:8080
# http://localhost:8080             structured state demo
# http://localhost:8080/kanban.html collaborative kanban
# http://localhost:8080/sequence.html collaborative text with cursors
```

Import the client from the engine's `/sdk/` mount (no install) and share one
room:

```js
import { Syncwell } from "/sdk/syncwell.js";
import { SequenceText } from "/sdk/text.js";
import { Awareness } from "/sdk/awareness.js";

const sw = new Syncwell("ws://localhost:8080/ws", "my-room", {
  clientId: "user-42",
  persist: true,
});
const text = new SequenceText(sw);
const peers = new Awareness(sw, { user: { id: "user-42", name: "Ada" } });

const render = (s) => console.log("document:", s);
sw.on("change", () => render(text.text()));
text.insertAt(0, "Hello, multiplayer");
peers.setSelection(5);
```

Read [Core concepts](/guide/concepts) for the model, or jump straight to the
[JavaScript SDK](/guide/sdk).

## Why self-host?

Syncwell is MIT-licensed and ships as one static binary with no database, cache,
or message broker. You own the data and the deployment. Start with
[operations](/self-hosting/operations) and the
[threat model](https://github.com/syncwell-labs/syncwell/blob/main/docs/operations/THREAT_MODEL.md).

::: tip Status
The engine and SDK are feature-complete through Phase 6. The Phase 7 docs site,
protocol v1 freeze, governance, and launch runbook are in the repository; hosted
deployment and publishing are owner-only. See the [roadmap](/project/roadmap)
and [launch runbook](/project/launch).
:::
