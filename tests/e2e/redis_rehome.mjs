import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { Syncwell } from "../../clients/javascript/syncwell.js";

const BIN = process.env.SYNCWELL_BIN || "bin/syncwell";
const REDIS_URL = process.env.SYNCWELL_REDIS_URL || "redis://127.0.0.1:6379";
const ROOM = `redis-rehome-${process.pid}`;
const children = [];
let sdk;
let tempDir;

function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close((err) => err ? reject(err) : resolve(port));
    });
  });
}

async function waitUntil(fn, timeoutMs, description) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await fn();
      if (value) return value;
    } catch (err) {
      lastError = err;
    }
    await sleep(100);
  }
  throw new Error(`timed out waiting for ${description}${lastError ? `: ${lastError}` : ""}`);
}

function startNode(id, port, cert, key, advertisements) {
  const args = [
    "-addr", `127.0.0.1:${port}`,
    "-tls-cert", cert,
    "-tls-key", key,
    "-insecure",
    "-allow-any-origin",
    "-cluster-mode", "redis",
    "-redis-url", REDIS_URL,
    "-node-id", id,
  ];
  for (const advertisement of advertisements) args.push("-node-advertise", advertisement);
  const child = spawn(BIN, args, { stdio: ["ignore", "pipe", "pipe"] });
  child.output = "";
  child.stdout.on("data", (chunk) => { child.output += chunk; });
  child.stderr.on("data", (chunk) => { child.output += chunk; });
  children.push(child);
  return child;
}

function startRelayNode(id, port, peerURLs) {
  const args = [
    "-addr", `127.0.0.1:${port}`,
    "-allow-any-origin",
    "-cluster-mode", "relay",
    "-cluster-insecure",
    "-cluster-peers", peerURLs.join(","),
    "-admin-token", "relay-rollback-test-secret",
    "-node-id", id,
    "-anti-entropy", "1s",
  ];
  const child = spawn(BIN, args, {
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, SYNCWELL_REDIS_URL: "", SYNCWELL_NODE_ADVERTISE: "" },
  });
  child.output = "";
  child.stdout.on("data", (chunk) => { child.output += chunk; });
  child.stderr.on("data", (chunk) => { child.output += chunk; });
  children.push(child);
  return child;
}

async function waitHealthy(port, child, scheme = "https") {
  await waitUntil(async () => {
    if (child.exitCode !== null) throw new Error(`server exited (${child.exitCode}): ${child.output}`);
    const response = await fetch(`${scheme}://localhost:${port}/healthz`);
    return response.ok;
  }, 10_000, `node health on ${port}`);
}

function rawWrites(port, clientID, writes, scheme = "wss") {
  const url = `${scheme}://localhost:${port}/ws?room=${encodeURIComponent(ROOM)}&client=${encodeURIComponent(clientID)}`;
  const ws = new WebSocket(url);
  let epoch = 1;
  let finished = false;
  let rejectResult;
  const expected = new Map(writes.map((write) => [write.key, write.value]));
  const acknowledgedWrites = new Map();
  const fail = (err) => {
    if (finished) return;
    finished = true;
    ws.close();
    rejectResult(err);
  };
  const acknowledged = new Promise((resolve, reject) => {
    rejectResult = reject;
    const timer = setTimeout(() => fail(new Error("timed out waiting for operation echo")), 5000);
    ws.addEventListener("error", () => fail(new Error("WebSocket connection failed")), { once: true });
    ws.addEventListener("message", (event) => {
      const frame = JSON.parse(String(event.data));
      if (frame.type === "snapshot") {
        epoch = frame.epoch || epoch;
        for (const write of writes) {
          ws.send(JSON.stringify({
            protocol: 1,
            type: "op",
            epoch,
            key: write.key,
            register: { v: write.value, t: { c: write.counter, n: clientID } },
          }));
        }
      } else if (frame.type === "op" && expected.has(frame.key)) {
        acknowledgedWrites.set(frame.key, frame.register?.v);
        clearTimeout(timer);
        finished = true;
        resolve({ ws, acknowledgedWrites });
      } else if (frame.type === "error") {
        clearTimeout(timer);
        fail(new Error(`server rejected write: ${JSON.stringify(frame.data)}`));
      }
    });
  });
  return { ws, acknowledged, acknowledgedWrites };
}

function watchRoom(port, clientID) {
  const ws = new WebSocket(`ws://localhost:${port}/ws?room=${encodeURIComponent(ROOM)}&client=${encodeURIComponent(clientID)}`);
  let state = {};
  let resolveReady;
  const ready = new Promise((resolve, reject) => {
    resolveReady = resolve;
    ws.addEventListener("error", () => reject(new Error("relay rollback observer failed to connect")), { once: true });
    ws.addEventListener("message", (event) => {
      const frame = JSON.parse(String(event.data));
      if (frame.type === "snapshot") {
        state = Object.fromEntries(Object.entries(frame.state || {}).map(([key, register]) => [key, register.v]));
        resolveReady();
      } else if (frame.type === "op") {
        state[frame.key] = frame.register?.v;
      }
    });
  });
  return { ws, ready, getState: () => ({ ...state }) };
}

async function main() {
	tempDir = mkdtempSync(join(tmpdir(), `syncwell-redis-rehome-${process.pid}-`));
	const tmp = tempDir;
	const cert = join(tmp, "cert.pem");
	const key = join(tmp, "key.pem");
  execFileSync("openssl", [
    "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
    "-keyout", key, "-out", cert,
    "-subj", "/CN=localhost",
    "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
  ], { stdio: "ignore" });
  const ports = await Promise.all([freePort(), freePort(), freePort()]);
  const ids = ["node-a", "node-b", "node-c"];
  const advertisements = ids.map((id, i) => `${id}=wss://localhost:${ports[i]}/ws`);
  let nodes = ids.map((id, i) => startNode(id, ports[i], cert, key, advertisements));
  await Promise.all(nodes.map((child, i) => waitHealthy(ports[i], child)));

  // Create the initial lease on node A and wait for a Redis-acknowledged write.
  const first = rawWrites(ports[0], "writer-a", [{ key: "acknowledged", value: "durable-value", counter: 1 }]);
  const firstResult = await first.acknowledged;
  firstResult.ws.close();

  // Queue a second write immediately before killing the owner. It may be
  // acknowledged or rejected by the race; only the first acknowledged write
  // is required to survive re-homing.
  const burstWrites = Array.from({ length: 32 }, (_, i) => ({ key: `in-flight-${i}`, value: `value-${i}`, counter: i + 1 }));
  const pending = rawWrites(ports[0], "writer-b", burstWrites);
  const pendingResult = await pending.acknowledged;
  const killedAt = Date.now();
  nodes[0].kill("SIGKILL");
  await new Promise((resolve) => nodes[0].once("exit", resolve));
  pendingResult.ws.close();

  sdk = new Syncwell(`wss://localhost:${ports[1]}/ws`, ROOM, {
    followOwner: true,
    clientId: "reader-b",
    maxReconnectDelay: 1000,
  });
  const recovered = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("re-homed room did not replay acknowledged state")), 25_000);
    sdk.on("change", (state) => {
      if (state.acknowledged === "durable-value") {
        clearTimeout(timer);
        resolve(state);
      }
    });
    sdk.on("error", (err) => {
      console.error(`follower error ${err.code}: ${err.message}`);
    });
  });
  sdk.connect();
  const state = await recovered;
  const elapsed = Date.now() - killedAt;
  assert.equal(state.acknowledged, "durable-value");
  for (const [key, value] of pendingResult.acknowledgedWrites) {
    assert.equal(state[key], value, `acknowledged in-flight write ${key} must survive owner death`);
  }
  assert.ok(elapsed < 20_000, `re-homing took ${elapsed}ms, expected lease expiry plus replay under 20s`);

  const route = await fetch(`https://localhost:${ports[1]}/route?room=${encodeURIComponent(ROOM)}`).then((res) => res.json());
  assert.equal(route.node, "node-b");
  assert.equal(new URL(route.url).port, String(ports[1]));
  console.log(JSON.stringify({ test: "redis-rehome", nodesStarted: 3, ownerKilled: "node-a", ownerAfterReplay: route.node, acknowledgedBurstWrites: pendingResult.acknowledgedWrites.size, elapsedMs: elapsed }));

  sdk.close();
  await Promise.all(nodes.slice(1).map((child) => new Promise((resolve) => {
    child.once("exit", resolve);
    child.kill("SIGTERM");
  })));
  const relayPeers = ports.map((port) => `http://127.0.0.1:${port}`);
  nodes = ids.map((id, i) => startRelayNode(id, ports[i], relayPeers.filter((_, peer) => peer !== i)));
  await Promise.all(nodes.map((child, i) => waitHealthy(ports[i], child, "http")));
  const relayObserver = watchRoom(ports[2], "relay-observer");
  await relayObserver.ready;
  const relayWriter = rawWrites(ports[0], "relay-writer", [
    { key: "rollback-mode", value: "relay-active", counter: 1 },
  ], "ws");
  const relayWrite = await relayWriter.acknowledged;
  relayWrite.ws.close();
  await waitUntil(() => relayObserver.getState()["rollback-mode"] === "relay-active", 5_000, "relay rollback event on the third node");
  relayObserver.ws.close();
  console.log(JSON.stringify({ test: "redis-relay-rollback", restartedNodes: nodes.length, mode: "relay", writeConverged: true }));
  await Promise.all(nodes.map((child) => new Promise((resolve) => {
    child.once("exit", resolve);
    child.kill("SIGTERM");
  })));
  rmSync(tmp, { recursive: true, force: true });
  tempDir = undefined;
}

try {
  await main();
} catch (err) {
  for (const [index, child] of children.entries()) {
    console.error(`node-${index} exit=${child.exitCode} output:\n${child.output}`);
  }
  throw err;
} finally {
  sdk?.close();
  for (const child of children) {
    if (child.exitCode === null) child.kill("SIGKILL");
  }
  if (tempDir) rmSync(tempDir, { recursive: true, force: true });
}
