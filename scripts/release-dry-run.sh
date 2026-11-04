#!/usr/bin/env bash
# Local release rehearsal. Builds the same OS/arch matrix as GoReleaser,
# writes checksums, and exercises the optional SBOM/signing tooling without
# contacting a registry, a transparency log, or a tag. Nothing is published.
#
# RELEASE_DRY_RUN_STRICT=1 turns a missing optional tool (goreleaser, syft,
# cosign) into a failure instead of a recorded skip.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

default_out() {
  local rel="$1" candidate="dist/$1"
  if mkdir -p "$candidate" 2>/dev/null && ( : >"$candidate/.write-test" ) 2>/dev/null; then
    rm -f "$candidate/.write-test"
    printf '%s\n' "$candidate"
  else
    mkdir -p "tmp/$rel"
    printf 'tmp/%s\n' "$rel"
  fi
}

OUT="${RELEASE_DRY_RUN_OUT:-$(default_out release-dry-run)}"
STRICT="${RELEASE_DRY_RUN_STRICT:-0}"
rm -rf "$OUT"
mkdir -p "$OUT/bin"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION=$(printf '%s' "$VERSION" | tr '/' '-')
COMMIT="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GOVERSION="$(go version 2>/dev/null || echo unknown)"
HOST="$(uname -srm 2>/dev/null || echo unknown)"

status=0
pass() { echo "OK   $*"; }
fail() { echo "FAIL $*"; status=1; }
skip() { echo "SKIP $*"; }

echo "release dry run: version=$VERSION commit=$COMMIT out=$OUT"

# 1. Reproducible cross-build of the released matrix.
targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)
built=0
for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  name="syncwell_${VERSION}_${goos}_${goarch}${ext}"
  if GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X syncwell/internal/version.Version=$VERSION -X syncwell/internal/version.Commit=$COMMIT -X syncwell/internal/version.Date=$DATE" \
    -o "$OUT/bin/$name" ./cmd/syncwell; then
    built=$((built + 1))
  else
    fail "build $name"
  fi
done
if [ "$built" -eq "${#targets[@]}" ]; then
  pass "cross-build matrix ($built targets)"
fi

# 2. Checksums over whatever was built.
hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1"
  else
    return 1
  fi
}
if [ "$built" -eq "${#targets[@]}" ]; then
  (
    cd "$OUT/bin" || exit 1
    for file in *; do
      hash_file "$file" || exit 1
    done
  ) >"$OUT/checksums.txt"
  if [ -s "$OUT/checksums.txt" ] && [ "$(wc -l <"$OUT/checksums.txt" | tr -d ' ')" -eq "$built" ]; then
    pass "checksums.txt ($built entries)"
  else
    fail "checksums.txt incomplete"
  fi
fi

# 3. Validate the GoReleaser config and report whether a snapshot could run.
GORELEASER_STATUS="skipped"
if command -v goreleaser >/dev/null 2>&1; then
  if goreleaser check >"$OUT/goreleaser-check.txt" 2>&1; then
    GORELEASER_STATUS="pass"
    pass "goreleaser check"
  else
    GORELEASER_STATUS="fail"
    fail "goreleaser check (see $OUT/goreleaser-check.txt)"
  fi
else
  skip "goreleaser (install: https://goreleaser.com/install/)"
  GORELEASER_STATUS="missing"
fi

# 4. SBOM generation. Scans the built binaries directory so the layout matches
# what the release workflow attaches to the GitHub release.
SYFT_STATUS="skipped"
if command -v syft >/dev/null 2>&1; then
  if syft dir:"$OUT/bin" -o "spdx-json=$OUT/sbom.spdx.json" >"$OUT/syft.txt" 2>&1; then
    SYFT_STATUS="pass"
    pass "SBOM $OUT/sbom.spdx.json"
  else
    SYFT_STATUS="fail"
    fail "syft (see $OUT/syft.txt)"
  fi
else
  skip "syft (install: https://github.com/anchore/syft#installation)"
  SYFT_STATUS="missing"
fi

# 5. Signing readiness. Never sign in a dry run: keyless signing needs the
# release workflow's OIDC identity and writes to the transparency log.
COSIGN_STATUS="skipped"
if command -v cosign >/dev/null 2>&1; then
  COSIGN_STATUS="ready"
  pass "cosign present; signing intentionally not executed"
else
  skip "cosign (install: https://docs.sigstore.dev/cosign/installation/)"
  COSIGN_STATUS="missing"
fi

if [ "$STRICT" = "1" ]; then
  [ "$GORELEASER_STATUS" = "missing" ] && status=1
  [ "$SYFT_STATUS" = "missing" ] && status=1
  [ "$COSIGN_STATUS" = "missing" ] && status=1
fi

# 6. Machine-readable and human-readable reports.
cat >"$OUT/meta.json" <<EOF
{
  "generatedAt": "$DATE",
  "version": "$VERSION",
  "commit": "$COMMIT",
  "goVersion": "$GOVERSION",
  "host": "$HOST",
  "targets": "$built/${#targets[@]}",
  "goreleaser": "$GORELEASER_STATUS",
  "syft": "$SYFT_STATUS",
  "cosign": "$COSIGN_STATUS",
  "published": false,
  "signed": false
}
EOF

cat >"$OUT/report.md" <<EOF
# Release dry run

- Generated: $DATE
- Version: \`$VERSION\`
- Commit: \`$COMMIT\`
- Host: $HOST
- Go: $GOVERSION
- Artifacts: \`$OUT/bin\`
- Checksums: \`$OUT/checksums.txt\`
- Published: **no**
- Signed: **no**

| Check | Result |
| --- | --- |
| cross-build matrix | $built/${#targets[@]} |
| GoReleaser config | $GORELEASER_STATUS |
| SBOM (syft) | $SYFT_STATUS |
| cosign readiness | $COSIGN_STATUS |

A dry run proves the build matrix and local tooling only. The real tag workflow
still owns registry pushes, GitHub release assets, keyless signing, and the
Render digest cutover (see \`docs/operations/RELEASE_CHECKLIST.md\`).
EOF

echo
echo "wrote $OUT/report.md"
if [ "$status" -ne 0 ]; then
  echo "release dry run found failures; see report above."
fi
exit "$status"
