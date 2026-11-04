import { execFileSync, spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import assert from "node:assert/strict";
import { test } from "node:test";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { waitHealthy } from "../helpers.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.join(__dirname, "..", "..");
const BIN = path.join(repoRoot, "bin", "syncwell");
const FIREFOX = process.env.FIREFOX_BIN || "/usr/bin/firefox";
const GECKODRIVER = process.env.GECKODRIVER_BIN || "/usr/bin/geckodriver";
const PORT = 8129;
const DRIVERS = [
  { port: 9527, websocketPort: 9223 },
  { port: 9528, websocketPort: 9224 },
];
const SECRET = "firefox-e2e-secret";
const ROOM = `firefox-e2e-${process.pid}-${Date.now().toString(36)}`;

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function readJson(response) {
  const text = await response.text();
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`invalid JSON response (${response.status}): ${text}`);
  }
}

function startDriver(bin, port, websocketPort) {
  const proc = spawn(
    bin,
    ["--host", "127.0.0.1", "--port", String(port), "--websocket-port", String(websocketPort)],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  let stderr = "";
  proc.stderr.on("data", (chunk) => {
    stderr += chunk;
  });
  return { proc, stderr: () => stderr, port };
}

async function waitForDriver(port, stderr) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/status`, {
        signal: AbortSignal.timeout(1000),
      });
      if (res.ok) return;
    } catch {
      // not listening yet
    }
    await sleep(100);
  }
  throw new Error(`geckodriver:${port} did not become ready: ${stderr()}`);
}

function webdriver(port, sessionId, method, route, body) {
  return fetch(`http://127.0.0.1:${port}/session/${sessionId}${route}`, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

async function createSession(driver) {
  const res = await fetch(`http://127.0.0.1:${driver.port}/session`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      capabilities: {
        alwaysMatch: {
          browserName: "firefox",
          pageLoadStrategy: "normal",
          webSocketUrl: true,
          "moz:firefoxOptions": {
            binary: FIREFOX,
            args: ["-headless"],
          },
        },
      },
    }),
  });
  const json = await readJson(res);
  assert.equal(res.status, 200, `Firefox session creation failed: ${JSON.stringify(json)}`);
  const sessionId = json.value?.sessionId;
  const bidiURL = json.value?.capabilities?.webSocketUrl;
  assert.ok(sessionId, `missing session id: ${JSON.stringify(json)}`);
  assert.ok(bidiURL, `missing WebDriver BiDi URL: ${JSON.stringify(json)}`);
  return { sessionId, bidiURL };
}

async function connectBidi(url) {
  const ws = new WebSocket(url);
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = reject;
    setTimeout(() => reject(new Error(`timed out connecting to BiDi at ${url}`)), 10000);
  });

  let nextId = 0;
  const pending = new Map();
  const events = [];
  ws.onmessage = (message) => {
    const msg = JSON.parse(message.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject, timer } = pending.get(msg.id);
      clearTimeout(timer);
      pending.delete(msg.id);
      if (msg.type === "success") resolve(msg.result);
      else reject(new Error(JSON.stringify(msg.error || msg)));
      return;
    }
    if (msg.type !== "event") return;
    const params = msg.params || {};
    if (msg.method === "log.entryAdded" && params.level === "error") {
      events.push({
        kind: "console",
        timestamp: params.timestamp,
        message: params.text || params.args?.map((arg) => arg.value).join(" ") || "",
      });
    } else if (msg.method === "network.fetchError") {
      events.push({
        kind: "network",
        timestamp: params.timestamp,
        url: params.request?.url || "",
        message: "fetchError",
      });
    } else if (msg.method === "network.responseCompleted") {
      const status = params.response?.status;
      if (status >= 400) {
        events.push({
          kind: "network",
          timestamp: params.timestamp,
          url: params.response?.url || params.request?.url || "",
          status,
          message: `HTTP ${status}`,
        });
      }
    }
  };

  function send(method, params = {}) {
    const id = ++nextId;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`BiDi ${method} timed out`));
      }, 10000);
      pending.set(id, { resolve, reject, timer });
      ws.send(JSON.stringify({ id, method, params }));
    });
  }

  await send("session.subscribe", {
    events: ["log.entryAdded", "network.fetchError", "network.responseCompleted"],
  });

  return {
    events,
    close: () => ws.close(),
    send,
  };
}

async function command(driver, sessionId, method, route, body) {
  const res = await webdriver(driver.port, sessionId, method, route, body);
  const json = await readJson(res);
  assert.equal(res.status, 200, `${method} ${route} failed: ${JSON.stringify(json)}`);
  return json.value;
}

async function execute(driver, sessionId, script) {
  return command(driver, sessionId, "POST", "/execute/sync", { script, args: [] });
}

async function executeAsync(driver, sessionId, script) {
  return command(driver, sessionId, "POST", "/execute/async", { script, args: [] });
}

async function waitFor(driver, sessionId, script, label, timeoutMs = 20000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      if (await execute(driver, sessionId, script)) return;
    } catch {
      // the page may be navigating or the socket may be reconnecting
    }
    await sleep(100);
  }
  throw new Error(`timed out waiting for ${label}: ${script}`);
}

async function navigate(driver, sessionId, url) {
  return command(driver, sessionId, "POST", "/url", { url });
}

async function setWindowRect(driver, sessionId, width, height) {
  return command(driver, sessionId, "POST", "/window/rect", { width, height });
}

function launchServer(args) {
  return spawn(BIN, args, { cwd: repoRoot, stdio: "ignore" });
}

async function stopProcess(proc) {
  if (!proc || proc.exitCode !== null) return;
  await new Promise((resolve) => {
    const timer = setTimeout(() => {
      if (proc.exitCode === null) proc.kill("SIGKILL");
    }, 5000);
    proc.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
    proc.kill("SIGTERM");
  });
}

function pageURL(token) {
  const u = new URL(`http://127.0.0.1:${PORT}/kanban.html`);
  u.searchParams.set("room", ROOM);
  u.searchParams.set("token", token);
  return u.toString();
}

function probeScript(wsBase, room, token) {
  return `
    const done = arguments[arguments.length - 1];
    const base = ${JSON.stringify(wsBase)};
    const room = ${JSON.stringify(room)};
    const token = ${JSON.stringify(token)};
    const url = base + "/ws?room=" + encodeURIComponent(room);
    function probe(withToken, bad) {
      return new Promise((resolve) => {
        let settled = false;
        let ws;
        const finish = (result) => {
          if (settled) return;
          settled = true;
          try { ws?.close(); } catch {}
          resolve(result);
        };
        try {
          if (bad) ws = new WebSocket(url, ["syncwell", "syncwell.token.bad-token"]);
          else if (withToken) ws = new WebSocket(url, ["syncwell", "syncwell.token." + token]);
          else ws = new WebSocket(url);
        } catch (err) {
          resolve("exception:" + err.message);
          return;
        }
        ws.onopen = () => finish("open");
        ws.onerror = () => finish("error");
        ws.onclose = (ev) => finish("close:" + ev.code);
        setTimeout(() => finish("timeout"), 4000);
      });
    }
    Promise.all([probe(false, false), probe(false, true), probe(true, false)])
      .then(([withoutToken, badToken, validToken]) => ({ withoutToken, badToken, validToken }))
      .then(done, (err) => done({ error: String(err) }));
  `;
}

function inWindow(timestamp, start, end) {
  return timestamp >= start - 1000 && timestamp <= end + 1000;
}

function isWebSocketURL(url) {
  try {
    return new URL(url).pathname === "/ws";
  } catch {
    return false;
  }
}

function expectedNoise(event, authStart, authEnd, offlineStart, offlineEnd) {
  if (event.kind === "network") {
    if (event.status === 404) {
      try {
        if (new URL(event.url).pathname === "/favicon.ico") return true;
      } catch {
        // malformed URL; treat as unexpected
      }
    }
    if (!isWebSocketURL(event.url)) return false;
    return (
      inWindow(event.timestamp, authStart, authEnd) ||
      inWindow(event.timestamp, offlineStart, offlineEnd)
    );
  }
  if (event.kind === "console") {
    if (!/WebSocket|establish a connection|ws:\/\//i.test(event.message || "")) return false;
    return (
      inWindow(event.timestamp, authStart, authEnd) ||
      inWindow(event.timestamp, offlineStart, offlineEnd)
    );
  }
  return false;
}

test(
  "Firefox ESR e2e: websocket auth, collaboration, reconnect, console and network",
  { timeout: 180000 },
  async () => {
    assert.ok(existsSync(BIN), `${BIN} missing; run \`make build\` first`);
    assert.ok(existsSync(FIREFOX), `Firefox not found at ${FIREFOX}; set FIREFOX_BIN`);
    assert.ok(existsSync(GECKODRIVER), `geckodriver not found at ${GECKODRIVER}; set GECKODRIVER_BIN`);
    const firefoxVersion = execFileSync(FIREFOX, ["--version"], { encoding: "utf8" }).trim();
    const geckodriverVersion = execFileSync(GECKODRIVER, ["--version"], { encoding: "utf8" })
      .split("\n")[0]
      .trim();
    console.log(`browser target: ${firefoxVersion} with ${geckodriverVersion}`);

    const token = execFileSync(BIN, ["-secret", SECRET, "-mint", ROOM, "-ttl", "10m"], {
      encoding: "utf8",
    }).trim();
    assert.ok(token, "failed to mint room token");

    const dataDir = mkdtempSync(path.join(os.tmpdir(), "syncwell-firefox-e2e-"));
    const serverArgs = [
      "-addr",
      `127.0.0.1:${PORT}`,
      "-secret",
      SECRET,
      "-origins",
      `http://127.0.0.1:${PORT}`,
      "-data",
      dataDir,
    ];
    let srv = launchServer(serverArgs);

    const drivers = DRIVERS.map(({ port, websocketPort }) =>
      startDriver(GECKODRIVER, port, websocketPort),
    );
    const sessions = [];
    try {
      await waitHealthy(PORT);
      for (const driver of drivers) {
        await waitForDriver(driver.port, driver.stderr);
        const { sessionId, bidiURL } = await createSession(driver);
        const bidi = await connectBidi(bidiURL);
        sessions.push({ driver, sessionId, bidi });
        await setWindowRect(driver, sessionId, 1280, 900);
      }

      const [first, second] = sessions;
      const url = pageURL(token);
      await navigate(first.driver, first.sessionId, url);
      await waitFor(
        first.driver,
        first.sessionId,
        "return document.getElementById('status')?.className === 'live'",
        "first tab websocket live",
      );

      const authStart = Date.now();
      const probes = await executeAsync(
        first.driver,
        first.sessionId,
        probeScript(`ws://127.0.0.1:${PORT}`, ROOM, token),
      );
      const authEnd = Date.now();
      assert.equal(probes.withoutToken, "error", `unauthenticated websocket: ${JSON.stringify(probes)}`);
      assert.equal(probes.badToken, "error", `bad-token websocket: ${JSON.stringify(probes)}`);
      assert.equal(probes.validToken, "open", `valid-token websocket: ${JSON.stringify(probes)}`);

      await navigate(second.driver, second.sessionId, url);
      await waitFor(
        second.driver,
        second.sessionId,
        "return document.getElementById('status')?.className === 'live'",
        "second tab websocket live",
      );

      const addCard = (label) => `
        const input = document.querySelector(".col[data-col='todo'] .add input");
        input.value = ${JSON.stringify(label)};
        input.dispatchEvent(new Event("input", { bubbles: true }));
        document.querySelector(".col[data-col='todo'] .add button").click();
        return true;
      `;
      await execute(first.driver, first.sessionId, addCard("Firefox E2E collab"));
      await waitFor(
        second.driver,
        second.sessionId,
        "return [...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Firefox E2E collab')",
        "second tab received created card",
      );

      const moveCard = `
        const card = [...document.querySelectorAll('.card')].find((el) => el.querySelector('.card-text')?.textContent === 'Firefox E2E collab');
        if (!card) return false;
        const move = card.querySelector('select.move');
        move.value = 'doing';
        move.dispatchEvent(new Event('change', { bubbles: true }));
        return true;
      `;
      assert.equal(await execute(second.driver, second.sessionId, moveCard), true);
      await waitFor(
        first.driver,
        first.sessionId,
        "return [...document.querySelectorAll('.col[data-col=\\'doing\\'] .card-text')].some((el) => el.textContent === 'Firefox E2E collab')",
        "first tab received moved card",
      );

      const deleteCard = `
        const card = [...document.querySelectorAll('.card')].find((el) => el.querySelector('.card-text')?.textContent === 'Firefox E2E collab');
        if (!card) return false;
        card.querySelector('button.del').click();
        return true;
      `;
      assert.equal(await execute(first.driver, first.sessionId, deleteCard), true);
      await waitFor(
        second.driver,
        second.sessionId,
        "return ![...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Firefox E2E collab')",
        "second tab received delete",
      );

      const offlineStart = Date.now();
      await stopProcess(srv);
      await waitFor(
        first.driver,
        first.sessionId,
        "return document.getElementById('status')?.className === 'offline'",
        "first tab detected disconnect",
      );
      await execute(first.driver, first.sessionId, addCard("Offline Firefox E2E"));
      await waitFor(
        first.driver,
        first.sessionId,
        "return [...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Offline Firefox E2E')",
        "offline card applied locally",
      );

      srv = launchServer(serverArgs);
      await waitHealthy(PORT);
      await waitFor(
        first.driver,
        first.sessionId,
        "return document.getElementById('status')?.className === 'live'",
        "first tab reconnected",
        30000,
      );
      await waitFor(
        second.driver,
        second.sessionId,
        "return document.getElementById('status')?.className === 'live'",
        "second tab reconnected",
        30000,
      );
      await waitFor(
        second.driver,
        second.sessionId,
        "return [...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Offline Firefox E2E')",
        "offline card converged to second tab",
        20000,
      );
      const offlineEnd = Date.now();

      assert.equal(
        await execute(second.driver, second.sessionId, `
          const card = [...document.querySelectorAll('.card')].find((el) => el.querySelector('.card-text')?.textContent === 'Offline Firefox E2E');
          if (!card) return false;
          card.querySelector('button.del').click();
          return true;
        `),
        true,
      );
      await waitFor(
        first.driver,
        first.sessionId,
        "return ![...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Offline Firefox E2E')",
        "reconnected tab received delete",
      );

      await setWindowRect(first.driver, first.sessionId, 390, 900);
      await navigate(first.driver, first.sessionId, url);
      await waitFor(
        first.driver,
        first.sessionId,
        "return document.getElementById('status')?.className === 'live'",
        "mobile websocket live",
      );
      assert.equal(
        await execute(
          first.driver,
          first.sessionId,
          "return document.documentElement.scrollWidth <= window.innerWidth + 1",
        ),
        true,
        "mobile layout overflowed horizontally",
      );

      await sleep(500);
      for (const { bidi } of sessions) {
        const unexpected = bidi.events.filter(
          (event) => !expectedNoise(event, authStart, authEnd, offlineStart, offlineEnd),
        );
        assert.deepEqual(
          unexpected,
          [],
          `unexpected Firefox console/network errors: ${JSON.stringify(unexpected)}`,
        );
      }
      console.log(
        `browser events: auth=${JSON.stringify(probes)}; captured=${sessions.reduce((n, s) => n + s.bidi.events.length, 0)}`,
      );
    } finally {
      for (const { driver, sessionId, bidi } of sessions) {
        try {
          bidi.close();
        } catch {
          // already closed
        }
        await webdriver(driver.port, sessionId, "DELETE", "").catch(() => {});
      }
      for (const driver of drivers) driver.proc.kill("SIGTERM");
      await stopProcess(srv);
      rmSync(dataDir, { recursive: true, force: true });
    }
  },
);
