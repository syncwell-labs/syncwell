// End-to-end proof of multi-client concurrent edits converging correctly.
//
// Boots the real Go server, drives multiple SDK clients over real WebSockets,
// simulates concurrent conflicting edits, and asserts convergence.
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { Syncwell } from "../sdk/syncwell.js";
import { waitHealthy } from "./helpers.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const BIN = path.join(__dirname, "..", "bin", "syncwell");
const PORT = 8124; // Use a different port from other e2e tests
const BASE = `ws://localhost:${PORT}/ws`;
const ROOM = "multi-client-e2e";

function newClient(id) {
  return new Syncwell(BASE, ROOM, { clientId: id });
}

async function main() {
  const srv = spawn(BIN, ["-addr", `127.0.0.1:${PORT}`], { stdio: "ignore" });
  let failed = null;
  try {
    await waitHealthy(PORT);

    const numClients = 5;
    const clients = Array.from({ length: numClients }, (_, i) => newClient(`client-${i}`));

    await Promise.all(clients.map(c => c.connect()));
    await sleep(250); // allow all to connect and receive initial snapshot

    // 1. Concurrent SET on the same key. The last writer should win.
    const conflictKey = "conflicted-key";
    const promises = clients.map((client, i) => {
      return sleep(i * 20).then(() => client.set(conflictKey, `value-from-client-${i}`));
    });
    await Promise.all(promises);
    await sleep(500); // allow server to process all messages

    const verifier = newClient("verifier-1");
    verifier.connect();
    await sleep(250);

    assert.equal(verifier.get(conflictKey), `value-from-client-${numClients - 1}`, "Last writer should win on concurrent set");
    console.log("PASS - Concurrent SET test passed.");

    // 2. Concurrent SET and DELETE on the same key.
    // Client 0 will set, Client 1 will delete after a short delay.
    // The delete should win if its clock is higher.
    const deleteKey = "delete-key";
    clients[0].set(deleteKey, "value-to-be-deleted");
    await sleep(50); // ensure set is processed
    clients[1].delete(deleteKey);
    await sleep(500); // allow delete to be processed

    assert.equal(verifier.get(deleteKey), undefined, "Key should be deleted");
    console.log("PASS - Concurrent SET/DELETE test passed.");
    
    // 3. All clients write to their own key, check for no data loss
    const soloKeyPrefix = "solo-key-";
    await Promise.all(clients.map((client, i) => {
      return client.set(`${soloKeyPrefix}${i}`, `solo-value-${i}`);
    }));
    await sleep(500);

    const finalState = verifier.live();
    for (let i = 0; i < numClients; i++) {
        assert.equal(finalState[`${soloKeyPrefix}${i}`], `solo-value-${i}`, `Data for client ${i} should not be lost`);
    }
    console.log("PASS - No data loss in concurrent writes to separate keys.");


    clients.forEach(c => c.close());
    verifier.close();
    console.log("PASS - Multi-client concurrent edits converged as expected.");
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
