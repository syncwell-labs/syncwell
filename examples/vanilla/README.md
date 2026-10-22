# Vanilla example

A single `index.html` with no framework: it imports `Syncwell` from
`/sdk/syncwell.js`, which the syncwell engine serves itself.

Serve it from the engine's static root (point `-static` at a directory that
contains this file and `clients/javascript/`), or copy `clients/javascript/` next to the file and run any
static server. Open the page in two windows to see live convergence.

If you prefer a bundler instead of the served SDK, install this repo's package
as `syncwell-client` — see [`../README.md`](../README.md).
