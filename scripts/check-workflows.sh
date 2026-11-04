#!/usr/bin/env bash
# Offline validation of the release-critical YAML in this repository.
# Checks that workflows parse, every third-party action is pinned to a full
# commit SHA, Go toolchains are pinned to an exact patch release, and no
# floating tool versions are requested. Runs actionlint when it is installed.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

status=0
pass() { echo "OK   $*"; }
fail() { echo "FAIL $*"; status=1; }
skip() { echo "SKIP $*"; }

files=()
for file in .github/workflows/*.yml .github/workflows/*.yaml .goreleaser.yaml render.yaml \
  .github/dependabot.yml .github/ISSUE_TEMPLATE/bug_report.yml \
  .github/ISSUE_TEMPLATE/config.yml .github/ISSUE_TEMPLATE/feature_request.yml; do
  [ -f "$file" ] && files+=("$file")
done

# 1. YAML syntax. PyYAML is preinstalled on the development and CI images; if
# it is absent this check is skipped rather than silently passing.
if python3 -c 'import yaml' >/dev/null 2>&1; then
  for file in "${files[@]}"; do
    if err=$(python3 - "$file" <<'PY' 2>&1
import sys
import yaml
with open(sys.argv[1], encoding="utf-8") as handle:
    yaml.safe_load(handle)
PY
    ); then
      pass "yaml $file"
    else
      fail "yaml $file: $err"
    fi
  done
else
  skip "YAML parse (python3 + PyYAML not installed)"
fi

# 2. Every `uses:` must reference a full 40-character commit SHA. Local actions
# (./path) are allowed because they ship in this repository.
unpinned=0
while IFS= read -r match; do
  [ -z "$match" ] && continue
  file=${match%%:*}
  rest=${match#*:}
  line=${rest%%:*}
  text=${rest#*:}
  ref=$(printf '%s\n' "$text" | sed -E \
    -e 's/^[[:space:]]*(-[[:space:]]*)?uses:[[:space:]]*//' \
    -e 's/[[:space:]]+#.*$//' \
    -e 's/[[:space:]]+$//')
  case "$ref" in
    ./*) continue ;;
  esac
  if ! printf '%s\n' "$ref" | grep -qE '@[0-9a-f]{40}$'; then
    fail "unpinned action $file:$line -> $ref"
    unpinned=$((unpinned + 1))
  fi
done < <(grep -HnE '^[[:space:]]*(-[[:space:]]*)?uses:' .github/workflows/*.yml .github/workflows/*.yaml 2>/dev/null || true)
[ "$unpinned" -eq 0 ] && pass "all uses: refs pinned to 40-char SHAs"

# 3. Do not opt into "latest" tool downloads: the release must be reproducible
# from the pinned actions and toolchain alone.
if grep -rnE '^[[:space:]]*check-latest:[[:space:]]*true' .github/workflows >/dev/null 2>&1; then
  fail "check-latest: true found in workflows"
else
  pass "no check-latest: true"
fi

# 4. Go toolchains must be full patch versions (e.g. 1.26.10), not 1.26 or stable.
bad_go=0
while IFS= read -r match; do
  [ -z "$match" ] && continue
  file=${match%%:*}
  rest=${match#*:}
  line=${rest%%:*}
  text=${rest#*:}
  value=$(printf '%s\n' "$text" | sed -E \
    -e 's/^[[:space:]]*go-version(-input)?:[[:space:]]*//' \
    -e 's/[[:space:]]+#.*$//' \
    -e 's/^"//' -e 's/"$//' \
    -e 's/[[:space:]]+$//')
  if ! printf '%s\n' "$value" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    fail "unpinned Go toolchain $file:$line -> $value (use a full patch version)"
    bad_go=$((bad_go + 1))
  fi
done < <(grep -HnE '^[[:space:]]*go-version(-input)?:' .github/workflows/*.yml .github/workflows/*.yaml 2>/dev/null || true)
[ "$bad_go" -eq 0 ] && pass "Go toolchains pinned to exact patch versions"

# 5. Full syntax/semantic lint when available.
if command -v actionlint >/dev/null 2>&1; then
  if actionlint; then
    pass "actionlint"
  else
    fail "actionlint"
  fi
else
  skip "actionlint (install: https://github.com/rhysd/actionlint#installation)"
fi

if [ "$status" -ne 0 ]; then
  echo
  echo "Workflow validation failed. Fix the items above before tagging a release."
fi
exit "$status"
