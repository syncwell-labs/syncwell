#!/usr/bin/env bash
# Local equivalent of the nightly soak job: a benchmark run plus a long
# loadtest against one real server, with the artifacts a reviewer needs.
# Defaults mirror .github/workflows/soak.yml (2000 clients, 10 minutes).
#
# Env overrides: CLIENTS, MAX_CLIENTS, DURATION_MS, INTERVAL_MS,
# LOADTEST_CLIENTS, ROOMS, LOADTEST_DURATION, WARMUP, LOADTEST_INTERVAL,
# SOAK_PORT, SOAK_OUT.
set -euo pipefail

cd "$(dirname "$0")/.." || exit 1

CLIENTS="${CLIENTS:-2000}"
MAX_CLIENTS="${MAX_CLIENTS:-$CLIENTS}"
DURATION_MS="${DURATION_MS:-600000}"
INTERVAL_MS="${INTERVAL_MS:-5}"
LOADTEST_CLIENTS="${LOADTEST_CLIENTS:-$CLIENTS}"
ROOMS="${ROOMS:-$((LOADTEST_CLIENTS / 20))}"
[ "$ROOMS" -ge 1 ] || ROOMS=1
LOADTEST_DURATION="${LOADTEST_DURATION:-10m}"
WARMUP="${WARMUP:-20s}"
LOADTEST_INTERVAL="${LOADTEST_INTERVAL:-100ms}"
BENCH_TIMEOUT="${BENCH_TIMEOUT:-20m}"
PORT="${SOAK_PORT:-8191}"

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

OUT="${SOAK_OUT:-$(default_out soak)/$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$OUT"

echo "soak: clients=$CLIENTS bench=${DURATION_MS}ms loadtest=$LOADTEST_CLIENTS/$ROOMS for $LOADTEST_DURATION"
echo "soak: artifacts -> $OUT"

make build loadtest-bin >/dev/null

echo "== bench =="
if command -v timeout >/dev/null 2>&1; then
  CLIENTS="$CLIENTS" MAX_CLIENTS="$MAX_CLIENTS" DURATION_MS="$DURATION_MS" \
    INTERVAL_MS="$INTERVAL_MS" BENCH_OUTPUT="$OUT/soak-bench.md" timeout "$BENCH_TIMEOUT" make bench
else
  CLIENTS="$CLIENTS" MAX_CLIENTS="$MAX_CLIENTS" DURATION_MS="$DURATION_MS" \
    INTERVAL_MS="$INTERVAL_MS" BENCH_OUTPUT="$OUT/soak-bench.md" make bench
fi

SERVER_LOG="$OUT/soak-server.log"
./bin/syncwell -addr "127.0.0.1:$PORT" -max-conns "$((MAX_CLIENTS + 500))" -ws-rate -1 >"$SERVER_LOG" 2>&1 &
SRV_PID=$!
cleanup() {
  kill "$SRV_PID" 2>/dev/null || true
  wait "$SRV_PID" 2>/dev/null || true
}
trap cleanup EXIT

healthy=0
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
    healthy=1
    break
  fi
  if ! kill -0 "$SRV_PID" 2>/dev/null; then
    echo "server exited before becoming healthy" >&2
    cat "$SERVER_LOG" >&2
    exit 1
  fi
  sleep 1
done
if [ "$healthy" -ne 1 ]; then
  echo "server did not become healthy on port $PORT" >&2
  cat "$SERVER_LOG" >&2
  exit 1
fi

echo "== loadtest =="
./bin/loadtest \
  -url "ws://127.0.0.1:$PORT/ws" \
  -clients "$LOADTEST_CLIENTS" \
  -rooms "$ROOMS" \
  -duration "$LOADTEST_DURATION" \
  -warmup "$WARMUP" \
  -interval "$LOADTEST_INTERVAL" \
  -server-pid "$SRV_PID" 2>&1 | tee "$OUT/soak-loadtest.txt"

cat >"$OUT/meta.json" <<EOF
{
  "generatedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "commit": "$(git rev-parse HEAD 2>/dev/null || echo unknown)",
  "goVersion": "$(go version 2>/dev/null || echo unknown)",
  "host": "$(uname -srm 2>/dev/null || echo unknown)",
  "bench": {
    "clients": $CLIENTS,
    "maxClients": $MAX_CLIENTS,
    "durationMs": $DURATION_MS,
    "intervalMs": $INTERVAL_MS
  },
  "loadtest": {
    "clients": $LOADTEST_CLIENTS,
    "rooms": $ROOMS,
    "duration": "$LOADTEST_DURATION",
    "warmup": "$WARMUP",
    "interval": "$LOADTEST_INTERVAL"
  },
  "port": $PORT
}
EOF

cat >"$OUT/report.md" <<EOF
# Local soak run

- Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Commit: \`$(git rev-parse HEAD 2>/dev/null || echo unknown)\`
- Host: $(uname -srm 2>/dev/null || echo unknown)
- Server port: $PORT
- Bench: $CLIENTS clients for $((DURATION_MS / 1000))s
- Loadtest: $LOADTEST_CLIENTS clients in $ROOMS rooms for $LOADTEST_DURATION

Artifacts:

- \`soak-bench.md\` — latency/throughput benchmark table
- \`soak-loadtest.txt\` — loadtest stdout (connect counts, latency, RSS)
- \`soak-server.log\` — server log for the loadtest window
- \`meta.json\` — exact parameters for reproduction

This is the local counterpart of the self-hosted \`soak\` workflow; it is
single-host and in-memory. Attach this directory to the P2-2 evidence bundle
alongside the hosted run.
EOF

echo "wrote $OUT/report.md"
