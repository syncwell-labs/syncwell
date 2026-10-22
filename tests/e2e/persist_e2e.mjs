// Durability proof: write through a client, hard-kill the server (SIGKILL),
// restart it pointing at the same data dir, and confirm a brand-new client
// receives the persisted document. Run: make persist
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Syncwell } from "../../clients/javascript/syncwell.js";
import { waitHealthy } from "../helpers.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(__dirname, "..", "..", "bin", "syncwell");
const PORT = 8127;
const BASE = `ws://localhost:${PORT}/ws`;

function startServer(dataDir) {
  return spawn(BIN, ["-addr", `127.0.0.1:${PORT}`, "-data", dataDir], { stdio: "ignore" });
}

async function main() {
  const dataDir = await mkdtemp(path.join(os.tmpdir(), "syncwell-data-"));
  let failed = null;
  let srv = startServer(dataDir);
  try {
    await waitHealthy(PORT);

    // 1) Write some state and let it flush to disk.
    const a = new Syncwell(BASE, "persist", { clientId: "A" });
    a.connect();
    await sleep(200);
    a.set("title", "survives restart");
    a.set("count", 42);
    await sleep(250);
    a.close();
    await sleep(100);

    // 2) Hard-kill the server (no graceful shutdown — proves on-write durability).
    srv.kill("SIGKILL");
    await sleep(300);

    // 3) Restart against the same data dir.
    srv = startServer(dataDir);
    await waitHealthy(PORT);

    // 4) A fresh client must receive the persisted document in its snapshot.
    const b = new Syncwell(BASE, "persist", { clientId: "B" });
    b.connect();
    await sleep(300);

    assert.equal(b.get("title"), "survives restart", "title must survive a kill -9 restart");
    assert.equal(b.get("count"), 42, "count must survive a kill -9 restart");
    b.close();
    console.log("PASS — document survived SIGKILL + restart:", JSON.stringify(b.live()));
  } catch (e) {
    failed = e;
  } finally {
    srv.kill("SIGKILL");
    await rm(dataDir, { recursive: true, force: true });
  }
  if (failed) {
    console.error("FAIL —", failed.message);
    process.exit(1);
  }
}

main();
