# Phase 7 launch runbook

This runbook separates what is implemented in the repository from what only the
repository owner can publish. Replace every `<...>` placeholder; do not commit a
live URL until it actually serves the release.

## 0. Prerequisites

- A release commit on `main` with green CI.
- Repository secrets/variables: `NPM_TOKEN`, `GITHUB_TOKEN` (provided),
  `SYNCWELL_SECRET`, `SYNCWELL_ADMIN_TOKEN`.
- A host for the demo. The zero-budget path is Render's free web service
  ([`render.yaml`](../../render.yaml)); the templates in `deploy/` cover VPS
  hosting. Render Free needs no credit card, sleeps after 15 minutes without
  traffic, and has no persistent disk.
- A DNS name or platform hostname for the demo and status page.

## 1. Tag and publish the release

```bash
git checkout main
git pull --ff-only
git tag -a v0.4.0 -m "Syncwell v0.4.0"
git push origin v0.4.0

git tag -a sdk-v0.4.0 -m "syncwell-client v0.4.0"
git push origin sdk-v0.4.0
```

Use the next semantic version, not necessarily `0.4.0`. Verify:

- GitHub Release has Linux/macOS/Windows archives for amd64/arm64, checksums, and
  SBOMs.
- GHCR has a multi-arch image for the tag with attestations.
- npm has `syncwell-client@X.Y.Z` with provenance.

## 2. Deploy the hosted demo pinned to the tag

The zero-budget path is Render's free web service. It needs no credit card, but
it sleeps after 15 minutes without inbound traffic (about a minute to wake) and
has no persistent disk, so demo rooms reset on restart.

1. In **Render Dashboard → New → Blueprint**, select this repository.
2. Apply [`render.yaml`](../../render.yaml): `plan: free`, region `singapore`,
   and the pinned image digest.
3. The free demo runs with `SYNCWELL_INSECURE=1` (no room tokens, throwaway
   in-memory rooms) and a generated `SYNCWELL_ADMIN_TOKEN`, so `/stats` and
   `/metrics` stay protected.
4. After the first deploy, set `SYNCWELL_ORIGINS` to the service URL and any
   custom domain, then restart the service.

Deploy by immutable image digest, never `latest`:

```text
ghcr.io/syncwell-labs/syncwell@sha256:<published-digest>
```

`SYNCWELL_DATA` is intentionally unset on the free plan (in-memory rooms). For a
paid deployment with a disk, set `SYNCWELL_DATA` to the mount path instead.

Then verify:

```bash
curl -fsS https://syncwell-demo.onrender.com/healthz
curl -fsS https://syncwell-demo.onrender.com/readyz
curl -fsS -H "Authorization: Bearer $SYNCWELL_ADMIN_TOKEN" \
  https://syncwell-demo.onrender.com/stats
```

For a token-protected deployment (paid host or real data), remove
`SYNCWELL_INSECURE`, set `SYNCWELL_SECRET`, then mint one long-lived token per
demo room and publish those URLs:

```bash
for room in demo kanban sequence-demo; do
  ./bin/syncwell -secret "$SYNCWELL_SECRET" -mint "$room" -ttl 8760h
done
# https://<demo-host>/?room=demo&token=<demo-token>
# https://<demo-host>/kanban.html?room=kanban&token=<kanban-token>
# https://<demo-host>/sequence.html?room=sequence-demo&token=<sequence-demo-token>
```

Keep the admin token out of every shared URL. Never use `SYNCWELL_INSECURE=1` on
a deployment holding real data.

Open `/`, `/kanban.html`, and `/sequence.html` in two browser windows. Confirm
edits, cursors, reconnect after a network toggle, and an admin room listing.

Pin the platform service to the tag/digest. A redeploy should not silently pull a
newer image.

## 3. Status page and uptime check

Use the platform health check against `/healthz` and a `/readyz` check for
draining. If a public status page is required, publish one that reflects:

- `GET /healthz` (process alive)
- `GET /readyz` (realtime ready)
- `GET /metrics` (admin-protected; do not expose publicly)
- demo WebSocket join/sync smoke

The repository variable is set to
`DEMO_URL=https://syncwell-demo.onrender.com`. The scheduled `Demo smoke`
workflow then checks `/healthz` and `/readyz` every 15 minutes and can be run
manually.

Record the public URL in `README.md` and the docs site only after the first green
smoke run.

## 4. Publish the docs site

1. Push the docs workflow to `main`.
2. In **Settings → Pages**, set **Source: GitHub Actions**.
3. Run **Docs → Deploy docs** (or push a change under `website/`).
4. Verify the deployed URL, navigation, local search, and code blocks.

The workflow builds with `DOCS_BASE=/syncwell/` by default and publishes to
https://syncwell-labs.github.io/syncwell/. If you use a custom domain or a
different repository name, set the repository variable `DOCS_BASE` (for example
`/` for a root domain) before deploying.

## 5. Publish the launch post and metrics

1. Update `docs/product/BLOG.md` with the final public URLs and the current release.
2. Run the benchmark/load test on the documented hardware, then update the
   README metrics table and `docs/product/BENCHMARKS.md`.
3. Remove or rewrite every "Still ahead" line in the README. Do not claim a
   hosted demo, npm package, GHCR image, or stars until the artifact exists.
4. Publish on Show HN and/or dev.to; link the post from the README and docs.
5. After 24-48 hours, record actual usage signals (stars, pulls, installs) only
   if they are measurable.

Suggested launch sequence:

- T-1 day: release tags, demo deploy, docs deploy, smoke tests.
- T-0: publish the post, then post to Show HN and relevant communities.
- T+1 day: triage issues, fix install blockers, publish a short follow-up.
- T+1 week: publish a retro with measured numbers and remaining limitations.

## 6. Rollback

- Demo: redeploy the previous immutable image tag/digest.
- Docs: revert the docs workflow commit or redeploy the previous Pages artifact.
- npm: deprecate the bad version and publish a patch; do not unpublish.
- Server: restore the last verified backup only after stopping writers. Follow
  `docs/operations/RESTORE_DRILL.md`.
