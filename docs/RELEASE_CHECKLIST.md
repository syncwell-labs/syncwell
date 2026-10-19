# Release checklist

Everything ships from this one repository: the engine (Go), the SDK (JS), the
demos, and the docs. This checklist is the owner-facing release path.

## 0. Pre-flight (must be true)

- [ ] `make all` is green locally.
- [ ] CI on `master` is green.
- [ ] No secrets in the tree or history (`git log -p | grep -E 'gh[pous]_|PRIVATE KEY'`).
- [ ] `docs/READINESS_AUDIT_2026-10-19.md` blockers that apply to the claim you
      are making are closed.

## 1. Version and tag

- [ ] Bump `internal/version` (or the build metadata) as needed.
- [ ] Write the release notes.
- [ ] `git tag vX.Y.Z && git push origin vX.Y.Z`.

## 2. Publish the SDK

The browser/Node client lives in `sdk/` and publishes as `syncwell-client`.

```bash
cd sdk
npm test
npm view syncwell-client version   # confirm the new version is not taken
npm publish --access public
```

Verify `package.json` still names the package `syncwell-client`, that `files`
lists only the runtime JS, and that `version` was bumped intentionally.

## 3. Publish the container image

```bash
docker build \
  --build-arg VERSION=vX.Y.Z \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t YOUR_REGISTRY/syncwell:vX.Y.Z .
docker push YOUR_REGISTRY/syncwell:vX.Y.Z
```

There is no build-time secret: the image is built entirely from this repo.

## 4. Deploy and verify

- [ ] Deploy the tagged image with `SYNCWELL_SECRET`, `SYNCWELL_ADMIN_TOKEN`,
      `SYNCWELL_ORIGINS`, and `SYNCWELL_DATA` set (see
      [`docs/DEPLOY.md`](DEPLOY.md)).
- [ ] `curl -s https://HOST/healthz` → `ok`.
- [ ] `curl -sI https://HOST/sdk/syncwell.js` → `200`.
- [ ] `/stats` and `/metrics` require the admin token.
- [ ] Open the kanban demo in two windows; edits propagate live.
- [ ] Partition/reconnect converges (see
      [`docs/PARTITION_CONVERGENCE_DEMO.md`](PARTITION_CONVERGENCE_DEMO.md)).

## 5. Update the README

- [ ] Replace the "still ahead" placeholder with the real values: hosted demo
      URL, npm package URL, release tag.
- [ ] If the benchmark table changed, regenerate it with `make bench` and note
      the conditions.
