// Proves tombstone GC bounds memory: create many deletes, let maintenance run,
// and confirm the room's tombstone count collapses while live state is intact.
// Run: make gc
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { Syncwell } from "../sdk/syncwell.js";
import { waitHealthy } from "./helpers.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(__dirname, "..", "bin", "syncwell");
const PORT = 8125;
const BASE = `ws://localhost:${PORT}/ws`;
const stats = async (room) => (await (await fetch(`http://localhost:${PORT}/stats`)).json())[room];

async function main() {
  // Low compaction threshold so maintenance (GC) runs frequently.
  const srv = spawn(BIN, ["-addr", `127.0.0.1:${PORT}`, "-compact", "8"], { stdio: "ignore" });
  let failed = null;
  try {
    await waitHealthy(PORT);
    const a = new Syncwell(BASE, "gc", { clientId: "A" });
    a.connect();
    await sleep(200);

    // Keep one key permanently live, then churn 40 throwaway keys (create+delete).
    a.set("keep", "i am alive");
    const CHURN = 40;
    for (let i = 0; i < CHURN; i++) {
      a.set(`tmp-${i}`, i);
      a.delete(`tmp-${i}`);
    }
    // Let the op stream drain and maintenance passes run.
    await sleep(800);

    const s = await stats("gc");
    console.log("room stats after churn:", JSON.stringify(s));

    assert.ok(s, "room stats present");
    assert.equal(a.get("keep"), "i am alive", "live key must survive GC");
    assert.equal(s.live, 1, "exactly one live key expected");
    // Without GC there would be ~40 tombstones; with causal-stability GC the
    // stable ones are collected. Allow a small tail of not-yet-stable tombstones.
    assert.ok(s.tombstones <= 5, `tombstones should be collected, got ${s.tombstones}`);
    assert.ok(s.lowWater > 0, "low-water mark should have advanced");

    a.close();
    console.log(`PASS — GC bounded tombstones to ${s.tombstones} (from ${CHURN}), lowWater=${s.lowWater}`);
  } catch (e) {
    failed = e;
  } finally {
    srv.kill("SIGKILL");
  }
  if (failed) {
    console.error("FAIL —", failed.message);
    process.exit(1);
  }
}

main();
