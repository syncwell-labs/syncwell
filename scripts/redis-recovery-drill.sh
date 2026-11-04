#!/usr/bin/env bash
# Three-node Redis ownership drill: start a dedicated AOF Redis, run the
# SIGKILL/re-home E2E, then restart the nodes in relay mode and verify that a
# write still converges on the third node. Produces RPO/RTO evidence locally.
#
# Set SYNCWELL_REDIS_URL to use an existing Redis (it must have
# appendonly=yes and appendfsync=everysec); otherwise a throwaway server is
# started under a temporary directory.
set -euo pipefail

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

OUT="${REDIS_DRILL_OUT:-$(default_out redis-drill)/$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$OUT"

command -v node >/dev/null 2>&1 || { echo "node is required" >&2; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo "openssl is required for the test certificate" >&2; exit 1; }

make build >/dev/null

REDIS_PID=""
REDIS_DIR=""
cleanup() {
  if [ -n "$REDIS_PID" ]; then
    kill "$REDIS_PID" 2>/dev/null || true
    wait "$REDIS_PID" 2>/dev/null || true
  fi
  [ -n "$REDIS_DIR" ] && rm -rf "$REDIS_DIR"
}
trap cleanup EXIT

redis_version() {
  redis-cli -u "$1" INFO server 2>/dev/null | sed -n 's/^redis_version://p' | tr -d '\r' || true
}

if [ -n "${SYNCWELL_REDIS_URL:-}" ]; then
  REDIS_URL="$SYNCWELL_REDIS_URL"
  REDIS_VERSION="$(redis_version "$REDIS_URL")"
  echo "redis: using existing $REDIS_URL ($REDIS_VERSION)"
else
  command -v redis-server >/dev/null 2>&1 || {
    echo "redis-server is required, or set SYNCWELL_REDIS_URL" >&2
    exit 1
  }
  command -v redis-cli >/dev/null 2>&1 || {
    echo "redis-cli is required to verify the temporary Redis" >&2
    exit 1
  }
  command -v python3 >/dev/null 2>&1 || {
    echo "python3 is required to pick a free port" >&2
    exit 1
  }
  REDIS_DIR="$(mktemp -d "${TMPDIR:-/tmp}/syncwell-redis-drill.XXXXXX")"
  REDIS_PORT="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
  cat >"$REDIS_DIR/redis.conf" <<EOF
bind 127.0.0.1
protected-mode yes
port $REDIS_PORT
dir $REDIS_DIR
appendonly yes
appendfsync everysec
save ""
EOF
  redis-server "$REDIS_DIR/redis.conf" --daemonize no >"$OUT/redis-server.log" 2>&1 &
  REDIS_PID=$!
  REDIS_URL="redis://127.0.0.1:$REDIS_PORT"
  ready=0
  for _ in $(seq 1 50); do
    if redis-cli -u "$REDIS_URL" ping 2>/dev/null | grep -q PONG; then
      ready=1
      break
    fi
    if ! kill -0 "$REDIS_PID" 2>/dev/null; then
      echo "redis-server exited" >&2
      cat "$OUT/redis-server.log" >&2
      exit 1
    fi
    sleep 0.2
  done
  if [ "$ready" -ne 1 ]; then
    echo "redis-server did not answer PING" >&2
    cat "$OUT/redis-server.log" >&2
    exit 1
  fi
  REDIS_VERSION="$(redis_version "$REDIS_URL")"
  echo "redis: started $REDIS_URL ($REDIS_VERSION, appendonly=yes appendfsync=everysec)"
fi

echo "running three-node SIGKILL/re-home drill..."
if SYNCWELL_BIN="$PWD/bin/syncwell" SYNCWELL_REDIS_URL="$REDIS_URL" \
  NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/redis_rehome.mjs 2>&1 | tee "$OUT/drill.log"; then
  drill_status=0
else
  drill_status=${PIPESTATUS[0]}
fi

elapsed=$(sed -n 's/.*"elapsedMs":\([0-9][0-9]*\).*/\1/p' "$OUT/drill.log" | tail -1)
burst=$(sed -n 's/.*"acknowledgedBurstWrites":\([0-9][0-9]*\).*/\1/p' "$OUT/drill.log" | tail -1)
owner=$(sed -n 's/.*"ownerAfterReplay":"\([^"]*\)".*/\1/p' "$OUT/drill.log" | tail -1)
relay=$(grep -c '"test":"redis-relay-rollback"' "$OUT/drill.log" 2>/dev/null || true)
[ -n "$relay" ] || relay=0

if [ "$drill_status" -eq 0 ]; then
  rpo="0 acknowledged writes lost"
  rto="${elapsed:-unknown} ms"
  result="pass"
else
  rpo="unverified (drill failed)"
  rto="unverified (drill failed)"
  result="fail"
fi

cat >"$OUT/meta.json" <<EOF
{
  "generatedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "commit": "$(git rev-parse HEAD 2>/dev/null || echo unknown)",
  "redisVersion": "$REDIS_VERSION",
  "redisUrl": "$REDIS_URL",
  "nodes": 3,
  "killSignal": "SIGKILL",
  "ownerKilled": "node-a",
  "ownerAfterReplay": "${owner:-unknown}",
  "acknowledgedBurstWrites": ${burst:-0},
  "rpo": "$rpo",
  "rtoMs": "${elapsed:-0}",
  "relayRollbackConverged": $([ "$relay" -gt 0 ] && echo true || echo false),
  "result": "$result"
}
EOF

cat >"$OUT/report.md" <<EOF
# Redis recovery drill

- Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Commit: \`$(git rev-parse HEAD 2>/dev/null || echo unknown)\`
- Redis: $REDIS_VERSION (\`appendonly=yes\`, \`appendfsync=everysec\`)
- Topology: 3 nodes, owner \`node-a\` killed with SIGKILL
- Owner after replay: \`${owner:-unknown}\`
- Acknowledged in-flight writes: ${burst:-0}
- **RPO:** $rpo
- **RTO:** $rto
- Relay rollback converged: $([ "$relay" -gt 0 ] && echo yes || echo no)
- Result: **$result**

Artifacts: \`drill.log\`, \`redis-server.log\` (when a temporary Redis was
started), \`meta.json\`.

This is the local, single-host counterpart of the provider failover acceptance
for P2-2. It does not replace the hosted run or the maintainer sign-off. A
passing result proves the lease re-home path loses no acknowledged write and
records the observed recovery time; a failing result marks both RPO and RTO as
unverified.
EOF

echo "wrote $OUT/report.md"
[ "$drill_status" -eq 0 ]
