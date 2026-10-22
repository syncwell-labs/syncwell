#!/usr/bin/env python3
"""axe-core WCAG 2.1 A/AA scan over the demo surfaces and the built docs site.

Run through `make a11y`, which builds `bin/syncwell` and the docs site first.
Needs `chromedriver` and Chrome/Chromium on PATH (both are preinstalled on
GitHub-hosted ubuntu runners).

What it covers, in light and dark:
  - demo pages `/`, `/kanban.html`, `/sequence.html`, `/admin`
  - the share dialog open on `/`
  - ten built website pages (landing, guides, self-hosting, reference, project)

Exit codes: 0 = clean, 1 = violations found, 2 = setup/runtime error.
A machine-readable report is written to the path given by --out.
"""

import argparse
import base64
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent
AXE_SRC = (REPO / "tests" / "vendor" / "axe.min.js").read_text()
TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]

DEMO_PORT = int(os.environ.get("AXE_DEMO_PORT", "18401"))
SITE_PORT = int(os.environ.get("AXE_SITE_PORT", "18402"))
CD_PORT = int(os.environ.get("AXE_CHROMEDRIVER_PORT", "18403"))
DEMO_BASE = f"http://127.0.0.1:{DEMO_PORT}"
SITE_BASE = f"http://127.0.0.1:{SITE_PORT}"
CD = f"http://127.0.0.1:{CD_PORT}"

DEMO_PAGES = ["/", "/kanban.html", "/sequence.html", "/admin"]
SITE_PAGES = [
    "/index.html",
    "/guide/getting-started.html",
    "/self-hosting/quickstart.html",
    "/self-hosting/configuration.html",
    "/self-hosting/operations.html",
    "/guide/python.html",
    "/guide/text-and-awareness.html",
    "/reference/protocol.html",
    "/reference/admin-and-webhooks.html",
    "/project/launch.html",
]


def die(msg, code=2):
    print(f"ERROR: {msg}", file=sys.stderr)
    sys.exit(code)


def req(method, path, body=None, timeout=180):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(CD + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            raw = resp.read()
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"{method} {path} -> HTTP {e.code}: {e.read()[:400]!r}") from None
    if not raw:
        return None
    parsed = json.loads(raw)
    if isinstance(parsed, dict):
        val = parsed.get("value")
        if isinstance(val, dict) and "error" in val:
            raise RuntimeError(f"{method} {path} -> WD error: {json.dumps(val)[:400]}")
        return val
    return parsed


def wait_http(url, what, timeout=20):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as resp:
                if resp.status < 500:
                    return
        except Exception:
            time.sleep(0.2)
    die(f"{what} did not become ready at {url}")


def exec_js(sid, script, args=None):
    return req("POST", f"/session/{sid}/execute/sync", {"script": script, "args": args or []})


def navigate(sid, url):
    req("POST", f"/session/{sid}/url", {"url": url})
    for _ in range(60):
        if exec_js(sid, "return document.readyState;") == "complete":
            break
        time.sleep(0.1)
    time.sleep(0.8)


def create_session(scheme):
    # forces prefers-color-scheme even when the host OS prefers the other one
    blink = "--blink-settings=preferredColorScheme=1" if scheme == "light" else "--force-dark-mode"
    args = ["--no-sandbox", "--headless=new", "--window-size=1440,900", "--disable-gpu", blink]
    body = {"capabilities": {"alwaysMatch": {
        "browserName": "chrome",
        "goog:chromeOptions": {"args": args},
    }}}
    return req("POST", "/session", body)["sessionId"]


def inject_axe(sid):
    exec_js(sid, "eval(arguments[0]); return typeof axe;", [AXE_SRC])
    if exec_js(sid, "return typeof axe;") != "object":
        die("axe-core injection failed")


def run_axe(sid):
    script = """
    return axe.run(document, {
      runOnly: { type: 'tag', values: %s }
    }).then(function (r) {
      return {
        url: r.url,
        violations: r.violations.map(function (v) {
          return {
            id: v.id, impact: v.impact, help: v.help,
            nodes: v.nodes.map(function (n) {
              return { target: n.target, html: n.html, failureSummary: n.failureSummary };
            })
          };
        })
      };
    });
    """ % json.dumps(TAGS)
    return exec_js(sid, script)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--out", default=str(REPO / "tests" / "a11y" / "axe-results.json"),
                    help="where to write the JSON report")
    args = ap.parse_args()

    binary = REPO / "bin" / "syncwell"
    dist = REPO / "website" / ".vitepress" / "dist"
    if not binary.exists():
        die(f"{binary} missing — run `make build` first (or use `make a11y`)")
    if not (dist / "index.html").exists():
        die(f"{dist} missing — run `make docs-build` first (or use `make a11y`)")
    if not shutil.which("chromedriver"):
        die("chromedriver not found on PATH")
    if not (shutil.which("google-chrome") or shutil.which("google-chrome-stable")
            or shutil.which("chromium") or shutil.which("chromium-browser")):
        die("Chrome/Chromium not found on PATH")

    procs = []
    try:
        procs.append(subprocess.Popen(
            [str(binary), "-addr", f"127.0.0.1:{DEMO_PORT}"],
            cwd=REPO, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        procs.append(subprocess.Popen(
            [sys.executable, "-m", "http.server", str(SITE_PORT), "--directory", str(dist)],
            cwd=REPO, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        procs.append(subprocess.Popen(
            ["chromedriver", f"--port={CD_PORT}", "--allowed-ips=", "--allowed-origins=*"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        wait_http(f"{DEMO_BASE}/healthz", "syncwell")
        wait_http(f"{SITE_BASE}/index.html", "docs site")
        wait_http(f"{CD}/status", "chromedriver")

        results, errors = [], []

        def measure(sid, label, scheme, before=None):
            try:
                if before:
                    exec_js(sid, before)
                    time.sleep(0.5)
                inject_axe(sid)
                r = run_axe(sid)
                results.append({"page": label, "scheme": scheme, **r})
                n = len(r["violations"])
                print(f"  {'OK ' if n == 0 else 'VIOL'} {label} [{scheme}] violations={n}")
            except Exception as e:  # noqa: BLE001 - report and fail at the end
                errors.append({"page": label, "scheme": scheme, "error": str(e)})
                print(f"  FAIL {label} [{scheme}]: {e}", file=sys.stderr)

        sid_light = create_session("light")
        sid_dark = create_session("dark")
        try:
            navigate(sid_light, DEMO_BASE + "/")
            if exec_js(sid_light, "return window.matchMedia('(prefers-color-scheme: dark)').matches;"):
                die("light session still prefers dark")
            navigate(sid_dark, DEMO_BASE + "/")
            if not exec_js(sid_dark, "return window.matchMedia('(prefers-color-scheme: dark)').matches;"):
                die("dark session does not prefer dark")

            print("== demo pages, light ==")
            for p in DEMO_PAGES:
                navigate(sid_light, DEMO_BASE + p)
                measure(sid_light, DEMO_BASE + p, "light")

            print("== demo pages, dark ==")
            for p in DEMO_PAGES:
                navigate(sid_dark, DEMO_BASE + p)
                measure(sid_dark, DEMO_BASE + p, "dark")

            share = ("document.querySelector('.welcome .dismiss')?.click();"
                     "document.getElementById('share-btn')?.click();")
            print("== share dialog ==")
            navigate(sid_light, DEMO_BASE + "/")
            measure(sid_light, DEMO_BASE + "/ (share dialog open)", "light", before=share)
            navigate(sid_dark, DEMO_BASE + "/")
            measure(sid_dark, DEMO_BASE + "/ (share dialog open)", "dark", before=share)

            print("== website, light ==")
            for p in SITE_PAGES:
                navigate(sid_light, SITE_BASE + p)
                exec_js(sid_light, "document.documentElement.classList.remove('dark'); return true;")
                time.sleep(0.3)
                measure(sid_light, SITE_BASE + p, "light")

            # VitePress switches schemes with the .dark class, not the media query
            print("== website, dark ==")
            for p in SITE_PAGES:
                navigate(sid_light, SITE_BASE + p)
                exec_js(sid_light, "document.documentElement.classList.add('dark'); return true;")
                time.sleep(0.3)
                measure(sid_light, SITE_BASE + p, "dark")
        finally:
            for sid in (sid_light, sid_dark):
                try:
                    req("DELETE", f"/session/{sid}")
                except Exception:
                    pass
    finally:
        for p in procs:
            p.terminate()
        for p in procs:
            try:
                p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                p.kill()

    report = {"results": results, "errors": errors}
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(report, indent=2))

    total = sum(len(r["violations"]) for r in results)
    print(f"\n{len(results)} runs, {total} violations, {len(errors)} errors -> {out}")
    if errors:
        die("some runs failed (see above)")
    if total:
        for r in results:
            for v in r["violations"]:
                print(f"\n{r['page']} [{r['scheme']}] {v['id']} ({v['impact']}): {v['help']}")
                for n in v["nodes"][:5]:
                    print(f"  {n['target']}\n    {(n.get('failureSummary') or '').splitlines()[-1] if n.get('failureSummary') else ''}")
        die("axe violations found", code=1)
    print("PASS — 0 axe violations (WCAG 2.1 A/AA)")


if __name__ == "__main__":
    main()
