// Builds and starts the production container image, then verifies the runtime
// serves health, SDK assets, and the WebSocket sync endpoint.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { setTimeout as sleep } from "node:timers/promises";
import { waitHealthy } from "./helpers.mjs";

const IMAGE = process.env.IMAGE || "syncwell:ci";
const CONTAINER = `syncwell-smoke-${process.pid}`;
const PORT = Number(process.env.PORT || 8130);
const VERSION = process.env.VERSION || "ci";
const COMMIT = process.env.COMMIT || "unknown";
const BUILD_DATE = process.env.BUILD_DATE || "unknown";

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



async function websocketSmoke() {
  const url = `ws://localhost:${PORT}/ws?room=container-smoke`;
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
      IMAGE,
    ]);
    await waitHealthy(PORT, { attempts: 60, interval: 250 });

    const sdk = await fetch(`http://localhost:${PORT}/sdk/syncwell.js`);
    assert.equal(sdk.status, 200, "SDK asset must be served from /sdk/");
    assert.match(await sdk.text(), /class Syncwell/);

    await websocketSmoke();
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
