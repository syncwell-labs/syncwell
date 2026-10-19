// Shared E2E helpers.
//
// `waitHealthy` intentionally uses node:http (not global fetch) with
// `Connection: close`. Node's global `fetch` and `WebSocket` share the undici
// dispatcher; a fetch probe against a server that is about to be killed can
// leave a pooled connection behind, and a subsequent websocket handshake to
// the restarted server can sit in CONNECTING. Keeping probes off the undici
// pool makes restarts deterministic. See
// docs/PROJECT_SCAN_ISSUES_2026-10-19.md (B2).
import http from "node:http";
import { setTimeout as sleep } from "node:timers/promises";

function probe(port, timeoutMs = 500) {
  return new Promise((resolve) => {
    const req = http.get(
      {
        host: "127.0.0.1",
        port,
        path: "/healthz",
        agent: false,
        headers: { Connection: "close" },
      },
      (res) => {
        res.resume();
        res.on("end", () => resolve(res.statusCode === 200));
      },
    );
    req.on("error", () => resolve(false));
    req.setTimeout(timeoutMs, () => {
      req.destroy();
      resolve(false);
    });
  });
}

export async function waitHealthy(port, { attempts = 60, interval = 100 } = {}) {
  for (let i = 0; i < attempts; i++) {
    if (await probe(port)) {
      await sleep(20); // let the listener settle before the socket handshake
      return;
    }
    await sleep(interval);
  }
  throw new Error(`server on :${port} did not become healthy`);
}
