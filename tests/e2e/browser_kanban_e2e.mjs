import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import assert from "node:assert/strict";
import { test } from "node:test";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const firefoxBin = process.env.FIREFOX_BIN || "/usr/bin/firefox";
const geckodriverBin = process.env.GECKODRIVER_BIN || "/usr/bin/geckodriver";
const demoURL = pathToFileURL(path.join(__dirname, "..", "..", "examples", "demos", "kanban.html")).href + "?test=1";

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function readJson(response) {
  const text = await response.text();
  try {
    return JSON.parse(text);
  } catch (err) {
    throw new Error(`invalid JSON response: ${text}`);
  }
}

async function startGeckodriver() {
  const proc = spawn(geckodriverBin, ["--host", "127.0.0.1", "--port", "4444"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  proc.stderr.on("data", (chunk) => {
    stderr += chunk;
  });
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (proc.exitCode !== null) {
      throw new Error(`geckodriver exited early (${proc.exitCode}): ${stderr}`);
    }
    try {
      const res = await fetch("http://127.0.0.1:4444/status", {
        signal: AbortSignal.timeout(1000),
      });
      if (res.ok) return { proc, stderr: () => stderr };
    } catch {
      // not listening yet
    }
    await sleep(100);
  }
  throw new Error(`geckodriver did not become ready in time: ${stderr}`);
}

async function withSession(fn) {
  const { proc, stderr } = await startGeckodriver();
  try {
    const create = await fetch("http://127.0.0.1:4444/session", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        capabilities: {
          alwaysMatch: {
            browserName: "firefox",
            "moz:firefoxOptions": {
              binary: firefoxBin,
              args: ["-headless"],
            },
          },
        },
      }),
    });
    assert.equal(create.status, 200, stderr());
    const created = await readJson(create);
    const sessionId = created.value?.sessionId || created.sessionId || created.value?.capabilities?.sessionId;
    assert.ok(sessionId, `missing session id: ${JSON.stringify(created)}`);
    try {
      await fn(sessionId);
    } finally {
      await fetch(`http://127.0.0.1:4444/session/${sessionId}`, { method: "DELETE" }).catch(() => {});
    }
  } finally {
    proc.kill("SIGTERM");
    await sleep(300);
  }
}

async function setWindowRect(sessionId, width, height) {
  const res = await fetch(`http://127.0.0.1:4444/session/${sessionId}/window/rect`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ width, height }),
  });
  assert.equal(res.status, 200);
}

async function navigate(sessionId, url) {
  const res = await fetch(`http://127.0.0.1:4444/session/${sessionId}/url`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url }),
  });
  assert.equal(res.status, 200);
}

async function execute(sessionId, script) {
  const res = await fetch(`http://127.0.0.1:4444/session/${sessionId}/execute/sync`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ script, args: [] }),
  });
  assert.equal(res.status, 200);
  const json = await readJson(res);
  return json.value;
}

async function waitFor(sessionId, script, timeoutMs = 8000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const value = await execute(sessionId, script);
    if (value) return value;
    await sleep(100);
  }
  throw new Error(`timed out waiting for: ${script}`);
}

test("kanban browser e2e passes at desktop and mobile widths", async () => {
  assert.ok(
    existsSync(firefoxBin),
    `firefox not found at ${firefoxBin}; install it or set FIREFOX_BIN`,
  );
  assert.ok(
    existsSync(geckodriverBin),
    `geckodriver not found at ${geckodriverBin}; install it or set GECKODRIVER_BIN`,
  );
  await withSession(async (sessionId) => {
    await setWindowRect(sessionId, 1280, 900);
    await navigate(sessionId, demoURL);
    assert.equal(await waitFor(sessionId, "return document.body.dataset.e2e === 'pass'"), true);
    assert.match(await execute(sessionId, "return document.body.textContent"), /browser e2e passed/);

    await setWindowRect(sessionId, 390, 900);
    await navigate(sessionId, demoURL);
    assert.equal(await waitFor(sessionId, "return document.body.dataset.e2e === 'pass'"), true);
    assert.match(await execute(sessionId, "return document.body.textContent"), /browser e2e passed/);
  });
});
