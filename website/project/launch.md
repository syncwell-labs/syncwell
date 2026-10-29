# Launch runbook

The repository contains the implementation and templates; the owner performs the
irreversible or credentialed steps. Never publish a URL or version before it
serves traffic.

The full canonical runbook is
[`docs/product/LAUNCH.md`](https://github.com/syncwell-labs/syncwell/blob/main/docs/product/LAUNCH.md).

## Order

1. **Tag the release.** Push `vX.Y.Z` and `sdk-vX.Y.Z`; verify GitHub release
   archives, checksums, SBOMs, GHCR image attestations, and npm provenance.
2. **Deploy the demo pinned to the tag.** Use
   `ghcr.io/syncwell-labs/syncwell:v0.5.0` (or the verified digest), set
   `SYNCWELL_SECRET`, `SYNCWELL_ADMIN_TOKEN`, `SYNCWELL_DATA`, and
   `SYNCWELL_ORIGINS`. Do not deploy `latest`.
3. **Verify.** `/healthz`, `/readyz`, admin `/stats`, a two-window edit, cursors,
   and an offline reconnect. If a room secret is set, mint long-lived
   room-scoped tokens for the public demos; never share the admin token.
4. **Monitor.** Set repository variable `DEMO_URL`; the scheduled
   `Demo smoke` workflow checks health/readiness. Add an external uptime check
   for the status page.
5. **Deploy docs.** Enable GitHub Pages with **GitHub Actions** source and run
   the Docs workflow. The default base is `/syncwell/`.
6. **Publish the post and metrics.** Update `docs/product/BLOG.md` with real URLs, run
   the benchmark on documented hardware, refresh the README numbers, and replace
   every remaining `<...>` placeholder with the live URLs.
7. **Rollback plan.** Redeploy the previous image digest; revert docs; deprecate
   a bad npm version and publish a patch (never unpublish); restore backups only
   after stopping writers.

## Status checks

`/healthz` is liveness. `/readyz` returns `503` during shutdown. `/metrics` is
admin-protected. The demo smoke workflow skips until `DEMO_URL` exists.

The owner-only board and release gates are in
[`docs/product/MILESTONES.md`](https://github.com/syncwell-labs/syncwell/blob/main/docs/product/MILESTONES.md).
