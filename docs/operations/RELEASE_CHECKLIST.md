# Release checklist

Everything ships from this one repository: the engine (Go), the SDK (JS), the
demos, and the docs. Releases are automated from tags; this checklist is the
owner-facing path and the manual fallback.

## 0. Pre-flight (must be true)

- [ ] `make all` is green locally (includes fmt, vet, race tests, coverage
      floors, JS tests, e2e, persistence, restore drill, GC, chaos, build).
- [ ] `make test-python` is green (polyglot client against the real server).
- [ ] `make sdk-typecheck sdk-build` is green, and `cd clients/javascript && npm pack --dry-run`
      lists `dist/`, all runtime `.js` files (map, text, awareness, storage),
      and the `.d.ts` files.
- [ ] `make docs-build` is green.
- [ ] Protocol contract tests pass; no v1 break without a v2 plan (see
      [`PROTOCOL_COMPATIBILITY.md`](../protocol/PROTOCOL_COMPATIBILITY.md)).
- [ ] CI on `main` is green (including browser E2E, Python E2E, and container
      smoke; the container smoke also checks `/admin` and the new SDK assets).
- [ ] No secrets in the tree or history
      (`git log -p | grep -E 'gh[pous]_|PRIVATE KEY'`).
- [ ] `docs/audits/READINESS_AUDIT_2026-10-19.md` blockers that apply to the claim you
      are making are closed.

## 1. Versions

- [ ] Bump `clients/javascript/package.json` `version` when publishing the client.
- [ ] Decide the engine tag (`vX.Y.Z`); `internal/version` is injected by
      GoReleaser and the Docker build, so no source edit is needed.
- [ ] Write the release notes (the changelog is generated from commits; edit the
      draft if needed).

## 2. Tag the engine (`vX.Y.Z`)

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

The `Release` workflow then:

1. runs GoReleaser — linux/darwin/windows × amd64/arm64 archives,
   `checksums.txt`, SPDX SBOMs, and a GitHub release;
2. builds and pushes the multi-arch image
   `ghcr.io/nil-vaghani/syncwell:X.Y.Z` (plus `X.Y` and `sha-*`) with
   provenance + SBOM attestations;
3. signs the image digest with cosign using GitHub OIDC (keyless).

Verify :

```bash
cosign verify ghcr.io/nil-vaghani/syncwell:X.Y.Z \
  --certificate-identity-regexp 'https://github.com/Nil-Vaghani/syncwell/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
docker run --rm -p 8080:8080 ghcr.io/nil-vaghani/syncwell:X.Y.Z -version
```

## 3. Publish the SDK (`sdk-vX.Y.Z`)

The tag **must** match `clients/javascript/package.json` `version`; the workflow fails otherwise.

```bash
cd clients/javascript && npm test && npm run typecheck && npm run build
cd .. && git tag sdk-vX.Y.Z && git push origin sdk-vX.Y.Z
```

The `Release SDK` workflow runs `npm ci`, tests, typechecks, builds, and
publishes `syncwell-client` with provenance:

```bash
npm publish --provenance --access public
```

`NPM_TOKEN` is a repository secret (an npm automation/granular token with publish
rights to `syncwell-client`). Manual fallback for a first publish:

```bash
cd clients/javascript
npm view syncwell-client version   # confirm the version is not taken
npm publish --access public
```

## 4. Deploy and verify

- [ ] Deploy the tagged image or binary using a template in
      [`docs/operations/DEPLOY.md`](./DEPLOY.md) (Compose/Render/nginx/Caddy/systemd).
- [ ] Set `SYNCWELL_SECRET`, `SYNCWELL_ADMIN_TOKEN`, and `SYNCWELL_ORIGINS`;
      set `SYNCWELL_DATA` and chown the volume only on a persistent host.
- [ ] `curl -s https://HOST/healthz` → `ok`.
- [ ] `curl -sI https://HOST/sdk/syncwell.js` → `200`.
- [ ] `/stats` and `/metrics` require the admin token.
- [ ] Open the kanban demo in two windows; edits propagate live.
- [ ] Partition/reconnect converges (see
      [`docs/architecture/PARTITION_CONVERGENCE_DEMO.md`](../architecture/PARTITION_CONVERGENCE_DEMO.md)).
- [ ] If running multiple nodes, check `syncwell_cluster_peer_up` and
      `syncwell_cluster_events_dropped_total` (see [`docs/operations/ALERTS.md`](./ALERTS.md)).
- [ ] Deploy the docs site from the `Docs` workflow and verify the Pages URL.
- [ ] Deploy the hosted demo pinned to the release tag/digest, set the
      `DEMO_URL` repository variable, and confirm the `Demo smoke` workflow.
      Follow [`LAUNCH.md`](../product/LAUNCH.md).

## 5. Update the README

- [ ] Replace the remaining placeholders with the real values: docs URL,
      hosted demo/status URL, npm package URL, release tag (the `<...>` slots
      in [`LAUNCH.md`](../product/LAUNCH.md) and the demo links in README/docs).
- [ ] Publish the refreshed launch post from [`BLOG.md`](../product/BLOG.md).
- [ ] If the benchmark table changed, run the `Benchmark` workflow (fixed
      parameters) or `CLIENTS=50 DURATION_MS=3000 make bench`, and note the
      conditions. Runners are shared; treat the numbers as indicative.
