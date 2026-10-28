// Builds and starts the production container image, then verifies the runtime
// serves health, SDK assets, and the WebSocket sync endpoint.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { waitHealthy } from "../helpers.mjs";

const IMAGE = process.env.IMAGE || "syncwell:ci";
const CONTAINER = `syncwell-smoke-${process.pid}`;
const PORT = Number(process.env.PORT || 8130);
const VERSION = process.env.VERSION || "ci";
const COMMIT = process.env.COMMIT || "unknown";
const BUILD_DATE = process.env.BUILD_DATE || "unknown";
const SECRET = "ci-smoke-secret";
const ADMIN = "ci-smoke-admin";

function run(cmd, args, opts = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { stdio: opts.stdio || "pipe" });
    let output = "";
    child.stdout?.on("data", (chunk) => { output += chunk; });
    child.stderr?.on("data", (chunk) => { output += chunk; });
    child.on("error", reject);
    child.on("close", (code) => {
      if (code === 0) resolve(output);
      else reject(new Error(`${cmd} ${args.join(" ")} failed (${code})\n${output}`));
    });
  });
}



async function websocketSmoke(token) {
  const url = `ws://localhost:${PORT}/ws?room=container-smoke&token=${encodeURIComponent(token)}`;
  const ws = new WebSocket(url);
  const first = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("websocket timed out")), 5000);
    ws.onmessage = (event) => {
      clearTimeout(timer);
      resolve(JSON.parse(event.data));
    };
    ws.onerror = () => {
      clearTimeout(timer);
      reject(new Error("websocket error"));
    };
  });
  assert.equal(first.type, "snapshot");
  ws.close();
}

async function main() {
  let failed = null;
  try {
    await run("docker", [
      "build",
      "--build-arg",
      `VERSION=${VERSION}`,
      "--build-arg",
      `COMMIT=${COMMIT}`,
      "--build-arg",
      `BUILD_DATE=${BUILD_DATE}`,
      "-t",
      IMAGE,
      ".",
    ]);
    await run("docker", [
      "run",
      "--rm",
      "-d",
      "--name",
      CONTAINER,
      "-p",
      `${PORT}:8080`,
      "-e",
      `SYNCWELL_SECRET=${SECRET}`,
      "-e",
      `SYNCWELL_ADMIN_TOKEN=${ADMIN}`,
      "-e",
      "SYNCWELL_ALLOW_ANY_ORIGIN=1",
      IMAGE,
    ]);
    await waitHealthy(PORT, { attempts: 60, interval: 250 });

    const sdk = await fetch(`http://localhost:${PORT}/sdk/syncwell.js`);
    assert.equal(sdk.status, 200, "SDK asset must be served from /sdk/");
    assert.match(await sdk.text(), /class Syncwell/);

    for (const asset of ["text.js", "awareness.js", "storage.js"]) {
      const res = await fetch(`http://localhost:${PORT}/sdk/${asset}`);
      assert.equal(res.status, 200, `${asset} must be served from /sdk/`);
    }

    const adminUI = await fetch(`http://localhost:${PORT}/admin`);
    assert.equal(adminUI.status, 200, "admin dashboard shell must be served");
    assert.match(await adminUI.text(), /Syncwell admin/);
    const adminRooms = await fetch(`http://localhost:${PORT}/admin/api/rooms`);
    assert.equal(adminRooms.status, 401, "admin room API must require the admin token");

    const stats = await fetch(`http://localhost:${PORT}/stats`);
    assert.equal(stats.status, 401, "admin endpoints must require the admin token");
    const authed = await fetch(`http://localhost:${PORT}/stats`, {
      headers: { Authorization: `Bearer ${ADMIN}` },
    });
    assert.equal(authed.status, 200, "admin token must unlock /stats");

    const token = (
      await run("docker", [
        "run",
        "--rm",
        "-e",
        `SYNCWELL_SECRET=${SECRET}`,
        IMAGE,
        "-mint",
        "container-smoke",
      ])
    ).trim();
    assert.ok(token.length > 20, "mint must print a room token");
    await websocketSmoke(token);
    console.log("PASS - container health, SDK, and websocket smoke checks passed");
  } catch (err) {
    failed = err;
  } finally {
    await run("docker", ["rm", "-f", CONTAINER]).catch(() => {});
  }
  if (failed) {
    console.error(failed.message);
    process.exit(1);
  }
}

main();
