#!/usr/bin/env bash
# Fail when a core package's statement coverage drops below its floor.
# Floors are deliberately a few points under the current measured value so
# normal refactors do not break the build, while a real regression does.
set -uo pipefail

declare -A MIN=(
  [syncwell/internal/crdt]=75
  [syncwell/internal/store]=75
  [syncwell/internal/server]=60
  [syncwell/internal/sequence]=60
)

out=$(go test ./... -cover -count=1 2>&1) || {
  printf '%s\n' "$out"
  echo "coverage run failed"
  exit 1
}

status=0
while read -r pkg pct; do
  [ -z "$pkg" ] && continue
  min=${MIN[$pkg]:-}
  [ -z "$min" ] && continue
  if [ -z "$pct" ]; then
    echo "FAIL $pkg: no coverage reported"
    status=1
  elif awk -v p="$pct" -v m="$min" 'BEGIN{exit !(p+0 < m+0)}'; then
    echo "FAIL $pkg: ${pct}% < ${min}%"
    status=1
  else
    echo "OK   $pkg: ${pct}% >= ${min}%"
  fi
done < <(printf '%s\n' "$out" |
  sed -n 's/.*[[:space:]]\(syncwell\/[^[:space:]]*\)[[:space:]].*coverage: \([0-9.]*\)%.*/\1 \2/p')

if [ "$status" -ne 0 ]; then
  echo
  echo "Coverage gate failed. Add tests or (deliberately) lower the floor in scripts/cover-check.sh."
fi
exit "$status"
