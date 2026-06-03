// End-to-end proof of the Phase 2.1 promise: a client edits while OFFLINE and
// converges with the rest of the room on reconnect, with no lost edits.
//
// Boots the real Go server, drives two SDK clients over real WebSockets,
// simulates a disconnect, and asserts convergence. Run: make e2e
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { Syncwell } from "../sdk/syncwell.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(__dirname, "..", "bin", "syncwell");
const PORT = 8123;
const BASE = `ws://localhost:${PORT}/ws`;

async function waitHealthy() {
  for (let i = 0; i < 50; i++) {
    try {
      const r = await fetch(`http://localhost:${PORT}/healthz`);
      if (r.ok) return;
    } catch {}
    await sleep(100);
  }
  throw new Error("server did not become healthy");
}

function newClient(id) {
  return new Syncwell(BASE, "e2e", { clientId: id });
}

async function main() {
  const srv = spawn(BIN, ["-addr", `:${PORT}`], { stdio: "ignore" });
  let failed = null;
  try {
    await waitHealthy();

    const a = newClient("A");
    const b = newClient("B");
    a.connect();
    b.connect();
    await sleep(250);

    // 1) Basic live sync: A writes, B sees it.
    a.set("k", "v1");
    await sleep(200);
    assert.equal(b.get("k"), "v1", "B should see A's online write");

    // 2) B goes OFFLINE and keeps editing locally; A edits the same key online.
    b.close();
    await sleep(100);
    b.set("offline-note", "written while disconnected"); // disjoint key
    b.set("k", "B-edit"); // concurrent conflict on "k"
    a.set("k", "A-edit");
    await sleep(150);

    // Local-first guarantee: B sees its own offline edit immediately, no network.
    assert.equal(b.get("offline-note"), "written while disconnected", "offline edit must apply locally");

    // 3) B reconnects → ships local state, pulls snapshot, converges.
    b.connect();
    await sleep(400);

    // The core guarantees: no lost edits, and both replicas agree exactly.
    assert.equal(a.get("offline-note"), "written while disconnected", "A must receive B's offline edit after reconnect");
    assert.deepEqual(a.live(), b.live(), "replicas must fully converge");
    assert.equal(a.get("k"), b.get("k"), "both replicas must agree on the conflicted key");
    // The conflict resolves deterministically by logical clock (LWW): B made two
    // offline edits so its clock is ahead, and its write wins. Last = last in
    // causal order, not wall-clock time (see DESIGN.md §3).
    assert.equal(a.get("k"), "B-edit", "logical-clock LWW: B's later (causally) write wins");

    a.close();
    b.close();
    console.log("PASS — offline edits converged:", JSON.stringify(a.live()));
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
