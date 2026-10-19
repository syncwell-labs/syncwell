import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawn } from "node:child_process";
import assert from "node:assert/strict";
import { once } from "node:events";
import { test } from "node:test";

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function startServer(bin, dataDir) {
  const proc = spawn(bin, ["-addr", "127.0.0.1:18080", "-data", dataDir], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  proc.stdout.on("data", (chunk) => {
    stdout += chunk;
  });
  proc.stderr.on("data", (chunk) => {
    stderr += chunk;
  });
  await sleep(1000);
  return { proc, stdout: () => stdout, stderr: () => stderr };
}

async function waitForOk(url, timeoutMs = 5000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(url);
      if (res.ok) return await res.text();
    } catch {}
    await sleep(100);
  }
  throw new Error(`timed out waiting for ${url}`);
}

test("restore drill documents the restart workflow", { skip: !process.env.SYNCWELL_BIN }, async () => {
  const dataDir = await mkdtemp(join(tmpdir(), "syncwell-restore-"));
  const url = "http://127.0.0.1:18080/healthz";
  let first;
  let second;
  try {
    first = await startServer(process.env.SYNCWELL_BIN, dataDir);
    assert.equal(await waitForOk(url), "ok");
    first.proc.kill("SIGTERM");
    await once(first.proc, "close");

    second = await startServer(process.env.SYNCWELL_BIN, dataDir);
    assert.equal(await waitForOk(url), "ok");
  } finally {
    if (first?.proc && !first.proc.killed) first.proc.kill("SIGTERM");
    if (second?.proc && !second.proc.killed) second.proc.kill("SIGTERM");
    await rm(dataDir, { recursive: true, force: true });
  }
});
