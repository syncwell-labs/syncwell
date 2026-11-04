# Load and soak results

The load harness is [`cmd/loadtest`](../../cmd/loadtest/main.go): it opens many
WebSocket clients, waits for each initial snapshot, publishes one timestamped
register per room at a fixed interval, and measures publish → receive latency
on the readers. It also samples the server's RSS from `/proc`.

## Reproduce

```bash
make build
make loadtest-bin   # builds bin/loadtest from cmd/loadtest

# terminal 1: in-memory single node
./bin/syncwell -addr 127.0.0.1:8191 -max-conns 10000 -ws-rate -1

# terminal 2
./bin/loadtest \
  -url ws://127.0.0.1:8191/ws \
  -clients 10000 -rooms 100 \
  -duration 60s -warmup 5s -interval 100ms \
  -server-pid "$(pgrep -f 'syncwell -addr 127.0.0.1:8191')"
```

For a token-protected server add `-secret "$SYNCWELL_SECRET"`; the harness
mints a room-scoped token per room. OIDC-only deployments need a token issued
by the identity provider.

`-ws-rate -1` disables the per-IP handshake rate limit for this isolated load
test, where every client shares loopback's IP. Do not use this setting on a
public production listener. The harness exits with an error if it cannot
connect every requested client, so a partial run cannot be mistaken for a
capacity result.

## Local soak artifacts

`make soak` is the local counterpart of `.github/workflows/soak.yml`. It runs
the benchmark and a long loadtest against one real server, then writes the
reviewable bundle:

```bash
make soak-quick   # 200 clients, 30 s bench + 30 s loadtest (harness smoke)
make soak         # 2000 clients, 10 min bench + 10 min loadtest
```

Artifacts land in `dist/soak/<timestamp>/` (or `tmp/soak/<timestamp>/` when
`dist/` is not writable): `soak-bench.md`, `soak-loadtest.txt`,
`soak-server.log`, `meta.json`, and `report.md`. Override the workload with
`CLIENTS`, `MAX_CLIENTS`, `DURATION_MS`, `INTERVAL_MS`, `LOADTEST_CLIENTS`,
`ROOMS`, `LOADTEST_DURATION`, `WARMUP`, `LOADTEST_INTERVAL`, `SOAK_PORT`, or
`SOAK_OUT`. This is still a single-host run; attach it next to, not instead of,
the dedicated-runner nightly artifact.

## Environment

| Item | Value |
| --- | --- |
| Host | Linux x86-64, 16 vCPU, 13 GiB RAM |
| Server | `syncwell` dev build, in-memory store, no TLS, no auth |
| Clients | same host as the server (loopback), Go harness |
| Load shape | 100 rooms × 100 clients; one publisher per room at 10 Hz; 99 readers |
| Fan-out | 9,900 deliveries/s at steady state |
| Duration | 60 s burst; 300 s soak |

## Results

| Run | Duration | Connect | p50 | p95 | p99 | Max | Delivered | Throughput | Server RSS |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Burst | 60 s | 10,000 / 10,000 | 17.6 ms | 30.5 ms | 36.6 ms | 66.5 ms | 6,492,000 | 108,200 msg/s | 323.1 MB |
| Soak | 300 s | 10,000 / 10,000 | 19.1 ms | 31.8 ms | 40.2 ms | 166.6 ms | 30,493,600 | 101,644 msg/s | 275.3 → 374.1 MB |

## Interpretation

- Latency includes client-side scheduling: server and 10,001 goroutines share
  one host, so these numbers are pessimistic compared with a dedicated server
  and real clients.
- The p99 tail is dominated by Go scheduler and TCP loopback bursts, not CRDT
  merge time. `/metrics` reports server-side apply latency separately
  (`syncwell_apply_duration_seconds`), which stays far below the end-to-end
  figures.
- Throughput is delivered messages per second. The publishers generate about
  1,000 writes/s; each write fans out to 99 readers.
- RSS is the server process only. In the soak it rose from 275 MiB after warmup
  to 374 MiB and stayed there (max == end), consistent with Go heap growth to a
  plateau rather than a leak. A continued climb across hours would indicate a
  leak; run the harness for longer on the target host before capacity claims.
- These runs use the in-memory store. With `-data`, expect lower write
  throughput and higher p99 when fsync is enabled; measure your own disk.

## Limitations

- No WAN latency, TLS, proxies, or persistence.
- One node; clustering adds relay CPU and network.
- 10k is the published target, not a hard cap. Raise `ulimit -n`, file
  descriptors, and kernel socket buffers before trying more. Set the server's
  `-max-conns` at least as high as the harness `-clients` count; `-max-clients`
  remains a separate per-room limit (100 clients per room in this run).
- The harness itself uses CPU and memory on the same host, so results are a
  lower bound on server capacity.
