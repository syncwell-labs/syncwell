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
const PORT = 8128;
const DRIVER_PORTS = [9525, 9526];
const SECRET = "browser-e2e-secret";
const ROOM = `browser-e2e-${process.pid}-${Date.now().toString(36)}`;

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function versionMajor(bin) {
  try {
    const out = execFileSync(bin, ["--version"], { encoding: "utf8", timeout: 10000 });
    const m = out.match(/(\d+)\.\d+\.\d+/);
    return m ? Number(m[1]) : null;
  } catch {
    return null;
  }
}

function selectBrowser() {
  const driver = process.env.CHROMEDRIVER_BIN || "/usr/bin/chromedriver";
  assert.ok(existsSync(driver), `chromedriver not found at ${driver}; set CHROMEDRIVER_BIN`);
  const driverMajor = Number(
    execFileSync(driver, ["--version"], { encoding: "utf8" }).match(/(\d+)\./)?.[1],
  );
  assert.ok(driverMajor, "could not determine chromedriver major version");

  const candidates = [
    { name: "Brave", bin: process.env.BRAVE_BIN || "/usr/bin/brave-browser" },
    { name: "Chromium", bin: process.env.CHROMIUM_BIN || "/usr/bin/chromium" },
    { name: "Chrome", bin: process.env.GOOGLE_CHROME_BIN || "/usr/bin/google-chrome" },
  ].filter((c) => existsSync(c.bin));

  if (process.env.BROWSER_BIN) {
    const forced = { name: path.basename(process.env.BROWSER_BIN), bin: process.env.BROWSER_BIN };
    assert.ok(existsSync(forced.bin), `BROWSER_BIN not found: ${forced.bin}`);
    return { driver, driverMajor, ...forced, major: versionMajor(forced.bin) };
  }

  const match = candidates.find((c) => versionMajor(c.bin) === driverMajor);
  const chosen = match || candidates[0];
  assert.ok(chosen, "no Brave/Chromium/Chrome binary found");
  return { driver, driverMajor, ...chosen, major: versionMajor(chosen.bin) };
}

async function readJson(response) {
  const text = await response.text();
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`invalid JSON response (${response.status}): ${text}`);
  }
}

function startDriver(bin, port) {
  const proc = spawn(bin, [`--port=${port}`], { stdio: ["ignore", "pipe", "pipe"] });
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
      const res = await fetch(`http://127.0.0.1:${port}/status`, { signal: AbortSignal.timeout(1000) });
      if (res.ok) return;
    } catch {
      // not listening yet
    }
    await sleep(100);
  }
  throw new Error(`chromedriver:${port} did not become ready: ${stderr()}`);
}

function webdriver(port, sessionId, method, route, body) {
  return fetch(`http://127.0.0.1:${port}/session/${sessionId}${route}`, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

async function createSession(driver, browser) {
  const res = await fetch(`http://127.0.0.1:${driver.port}/session`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      capabilities: {
        alwaysMatch: {
          browserName: "chrome",
          pageLoadStrategy: "normal",
          "goog:chromeOptions": {
            binary: browser.bin,
            args: [
              "--headless=new",
              "--no-sandbox",
              "--disable-gpu",
              "--disable-dev-shm-usage",
              "--no-first-run",
              "--remote-debugging-pipe",
            ],
          },
          "goog:loggingPrefs": { browser: "ALL" },
        },
      },
    }),
  });
  const json = await readJson(res);
  assert.equal(
    res.status,
    200,
    `session creation failed for ${browser.name} ${browser.major} with chromedriver ${browser.driverMajor}: ${JSON.stringify(json)}`,
  );
  const sessionId = json.value?.sessionId;
  assert.ok(sessionId, `missing session id: ${JSON.stringify(json)}`);
  return sessionId;
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

async function executeAsync(driver, sessionId, script, timeout = 15000) {
  return command(driver, sessionId, "POST", "/execute/async", { script, args: [] }, timeout);
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

async function browserLogs(driver, sessionId) {
  const value = await command(driver, sessionId, "POST", "/se/log", { type: "browser" });
  return Array.isArray(value) ? value : [];
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

function expectedAuthLog(entry, start, end) {
  return (
    entry.timestamp >= start &&
    entry.timestamp <= end &&
    /401|unauthorized|authentication|handshake|no valid credentials/i.test(entry.message)
  );
}

function expectedOfflineLog(entry, start, end) {
  return (
    entry.timestamp >= start &&
    entry.timestamp <= end &&
    /WebSocket|ERR_INTERNET_DISCONNECTED|ERR_NETWORK_CHANGED|ERR_CONNECTION/i.test(entry.message)
  );
}

test(
  "Chromium/Brave e2e: websocket auth, collaboration, reconnect, console and network",
  { timeout: 180000 },
  async () => {
    assert.ok(existsSync(BIN), `${BIN} missing; run \`make build\` first`);
    const browser = selectBrowser();
    console.log(
      `browser target: ${browser.name} ${browser.major} with chromedriver ${browser.driverMajor}`,
    );

    const token = execFileSync(
      BIN,
      ["-secret", SECRET, "-mint", ROOM, "-ttl", "10m"],
      { encoding: "utf8" },
    ).trim();
    assert.ok(token, "failed to mint room token");

    const dataDir = mkdtempSync(path.join(os.tmpdir(), "syncwell-browser-e2e-"));
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

    const drivers = DRIVER_PORTS.map((port) => startDriver(browser.driver, port));
    const sessions = [];
    try {
      await waitHealthy(PORT);
      for (const driver of drivers) {
        await waitForDriver(driver.port, driver.stderr);
        const sessionId = await createSession(driver, browser);
        sessions.push({ driver, sessionId });
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
      await execute(first.driver, first.sessionId, addCard("Browser E2E collab"));
      await waitFor(
        second.driver,
        second.sessionId,
        "return [...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Browser E2E collab')",
        "second tab received created card",
      );

      const moveCard = `
        const card = [...document.querySelectorAll('.card')].find((el) => el.querySelector('.card-text')?.textContent === 'Browser E2E collab');
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
        "return [...document.querySelectorAll('.col[data-col=\\'doing\\'] .card-text')].some((el) => el.textContent === 'Browser E2E collab')",
        "first tab received moved card",
      );

      const deleteCard = `
        const card = [...document.querySelectorAll('.card')].find((el) => el.querySelector('.card-text')?.textContent === 'Browser E2E collab');
        if (!card) return false;
        card.querySelector('button.del').click();
        return true;
      `;
      assert.equal(await execute(first.driver, first.sessionId, deleteCard), true);
      await waitFor(
        second.driver,
        second.sessionId,
        "return ![...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Browser E2E collab')",
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
      await execute(first.driver, first.sessionId, addCard("Offline browser E2E"));
      await waitFor(
        first.driver,
        first.sessionId,
        "return [...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Offline browser E2E')",
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
        "return [...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Offline browser E2E')",
        "offline card converged to second tab",
        20000,
      );
      const offlineEnd = Date.now();

      assert.equal(
        await execute(second.driver, second.sessionId, `
          const card = [...document.querySelectorAll('.card')].find((el) => el.querySelector('.card-text')?.textContent === 'Offline browser E2E');
          if (!card) return false;
          card.querySelector('button.del').click();
          return true;
        `),
        true,
      );
      await waitFor(
        first.driver,
        first.sessionId,
        "return ![...document.querySelectorAll('.card-text')].some((el) => el.textContent === 'Offline browser E2E')",
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

      for (const { driver, sessionId } of sessions) {
        const logs = await browserLogs(driver, sessionId);
        const unexpected = logs.filter((entry) => {
          if (entry.level !== "SEVERE") return false;
          // The demo has no favicon; Chromium reports the unavoidable 404 as a
          // network error. Every other resource/network failure is a failure.
          if (/favicon\.ico/i.test(entry.message) && /404/.test(entry.message)) return false;
          if (expectedAuthLog(entry, authStart, authEnd)) return false;
          if (expectedOfflineLog(entry, offlineStart, offlineEnd)) return false;
          return true;
        });
        assert.deepEqual(
          unexpected,
          [],
          `unexpected browser console/network errors: ${JSON.stringify(unexpected)}`,
        );
      }
    } finally {
      for (const { driver, sessionId } of sessions) {
        await webdriver(driver.port, sessionId, "DELETE", "").catch(() => {});
      }
      for (const driver of drivers) driver.proc.kill("SIGTERM");
      await stopProcess(srv);
      rmSync(dataDir, { recursive: true, force: true });
    }
  },
);
