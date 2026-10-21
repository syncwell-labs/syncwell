// Restore drill: prove the offline backup/restore path recovers a room after
// total data-directory loss, and that a corrupt backup is rejected instead of
// silently overwriting live data. Run: make restore-drill
import { execFile, spawn } from "node:child_process";
import { once } from "node:events";
import { cp, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { setTimeout as sleep } from "node:timers/promises";
import assert from "node:assert/strict";
import { Syncwell } from "../sdk/syncwell.js";
import { waitHealthy } from "./helpers.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const BIN = process.env.SYNCWELL_BIN || path.join(__dirname, "..", "bin", "syncwell");
const PORT = 8128;
const BASE = `ws://localhost:${PORT}/ws`;
const run = promisify(execFile);

function startServer(dataDir) {
  return spawn(BIN, ["-addr", `127.0.0.1:${PORT}`, "-data", dataDir], { stdio: "ignore" });
}

async function stopServer(proc) {
  if (!proc || proc.exitCode !== null) return;
  proc.kill("SIGKILL");
  await once(proc, "close");
}

async function main() {
  const root = await mkdtemp(path.join(os.tmpdir(), "syncwell-restore-"));
  const dataDir = path.join(root, "data");
  const backupDir = path.join(root, "backup");
  const corruptDir = path.join(root, "corrupt");
  const scratchData = path.join(root, "scratch");
  let srv = null;
  let failed = null;

  try {
    // 1) Produce some durable state, then hard-kill the server.
    srv = startServer(dataDir);
    await waitHealthy(PORT);
    const a = new Syncwell(BASE, "restore-drill", { clientId: "A" });
    a.connect();
    await sleep(200);
    a.set("title", "restored from backup");
    a.set("count", 7);
    await sleep(250);
    a.close();
    await sleep(100);
    await stopServer(srv);
    srv = null;

    // 2) Offline backup (flushes + fsyncs, writes a checksummed manifest).
    const backup = await run(BIN, ["-data", dataDir, "-backup", backupDir]);
    assert.match(backup.stdout, /backed up \d+ files/, backup.stdout);
    const verify = await run(BIN, ["-verify-backup", backupDir]);
    assert.match(verify.stdout, /backup ok/, verify.stdout);

    // 3) A tampered backup must fail verification and must not restore.
    await cp(backupDir, corruptDir, { recursive: true });
    const manifest = JSON.parse(await readFile(path.join(corruptDir, "MANIFEST.json"), "utf8"));
    const logPath = path.join(corruptDir, manifest.files[0].name);
    const bytes = await readFile(logPath);
    bytes[bytes.length - 1] ^= 0xff;
    await writeFile(logPath, bytes);
    await assert.rejects(run(BIN, ["-verify-backup", corruptDir]), /verify backup|checksum/i);
    await assert.rejects(
      run(BIN, ["-restore", corruptDir, "-data", scratchData]),
      /restore|checksum/i,
    );

    // 4) Lose the data directory entirely, then restore from the backup.
    await rm(dataDir, { recursive: true, force: true });
    const restored = await run(BIN, ["-restore", backupDir, "-data", dataDir]);
    assert.match(restored.stdout, /restored \d+ files/, restored.stdout);

    // 5) A fresh server on the restored directory must serve the document.
    srv = startServer(dataDir);
    await waitHealthy(PORT);
    const b = new Syncwell(BASE, "restore-drill", { clientId: "B" });
    b.connect();
    await sleep(300);
    assert.equal(b.get("title"), "restored from backup", "title must survive backup + restore");
    assert.equal(b.get("count"), 7, "count must survive backup + restore");
    b.close();
    console.log("PASS — document recovered from backup:", JSON.stringify(b.live()));
  } catch (e) {
    failed = e;
  } finally {
    await stopServer(srv);
    await rm(root, { recursive: true, force: true });
  }
  if (failed) {
    console.error("FAIL —", failed.stderr || failed.message);
    process.exit(1);
  }
}

main();
