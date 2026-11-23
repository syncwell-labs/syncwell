import { test } from "node:test";
import assert from "node:assert/strict";
import { mountDevTools, escapeHtml, sanitizeColor } from "./devtools.js";

function fakeSyncwell() {
  const handlers = {};
  return {
    room: "dev-room",
    clientId: "dev-client",
    clock: { counter: 5 },
    epoch: 1,
    doc: {
      snapshot() {
        return { key1: { v: "val1", t: { c: 1, n: "dev-client" } } };
      },
    },
    live() {
      return { key1: "val1" };
    },
    on(event, cb) {
      (handlers[event] ||= []).push(cb);
    },
    off(event, cb) {
      handlers[event] = (handlers[event] || []).filter((fn) => fn !== cb);
    },
  };
}

test("mountDevTools does not throw in Node / SSR environment", () => {
  const sw = fakeSyncwell();
  const devtools = mountDevTools(sw);
  assert.ok(devtools);
  assert.equal(typeof devtools.unmount, "function");
  assert.equal(typeof devtools.open, "function");
  assert.equal(typeof devtools.close, "function");
  devtools.open();
  devtools.close();
  devtools.unmount();
});

test("devtools sanitizes room, client IDs, user names, and peer colors against XSS", () => {
  assert.equal(
    escapeHtml('<script>alert("xss")</script>'),
    "&lt;script&gt;alert(&quot;xss&quot;)&lt;/script&gt;"
  );
  assert.equal(
    escapeHtml('" onmouseover="alert(1)'),
    "&quot; onmouseover=&quot;alert(1)"
  );

  assert.equal(sanitizeColor("#ff0000"), "#ff0000");
  assert.equal(sanitizeColor("#abc"), "#abc");
  assert.equal(sanitizeColor("rgb(255, 0, 0)"), "rgb(255, 0, 0)");
  assert.equal(sanitizeColor("rgba(0, 0, 0, 0.5)"), "rgba(0, 0, 0, 0.5)");
  assert.equal(sanitizeColor("hsl(120, 100%, 50%)"), "hsl(120, 100%, 50%)");
  assert.equal(sanitizeColor('red; background-image: url("javascript:alert(1)")'), "#38bdf8");
  assert.equal(sanitizeColor('" onmouseover="alert(1)'), "#38bdf8");
  assert.equal(sanitizeColor("expression(alert(1))"), "#38bdf8");

  const elements = [];
  const fakeElement = () => {
    const el = {
      style: {},
      children: [],
      appendChild(child) { el.children.push(child); return child; },
      querySelector(sel) { return fakeElement(); },
      set innerHTML(html) { el._html = html; },
      get innerHTML() { return el._html || ""; },
    };
    elements.push(el);
    return el;
  };

  const originalDoc = globalThis.document;
  const originalWin = globalThis.window;
  globalThis.document = {
    createElement(tag) { return fakeElement(); },
    body: fakeElement(),
  };
  globalThis.window = {
    addEventListener() {},
    removeEventListener() {},
  };

  try {
    const maliciousSw = {
      room: '<script>alert("room")</script>',
      clientId: '<img src=x onerror=alert("id")>',
      clock: { counter: 1 },
      epoch: 1,
      doc: { snapshot: () => ({}) },
      live: () => ({}),
      on() {},
      off() {},
    };
    const awareness = {
      getStates() {
        return [
          {
            clientId: '<script>alert("peer")</script>',
            color: 'blue; xss: expression(1);"><script>alert(2)</script>',
            user: { name: '<b onmouseover="alert(1)">Hacker</b>' },
            cursor: { index: 0 },
          },
        ];
      },
      on() {},
      off() {},
    };

    const dt = mountDevTools(maliciousSw, { awareness });
    dt.open();

    const stateBody = elements.find((el) => el.innerHTML && el.innerHTML.includes("Room:"));
    assert.ok(stateBody);
    assert.ok(!stateBody.innerHTML.includes('<script>alert("room")</script>'));
    assert.ok(stateBody.innerHTML.includes("&lt;script&gt;alert(&quot;room&quot;)&lt;/script&gt;"));
    assert.ok(!stateBody.innerHTML.includes('<img src=x onerror=alert("id")>'));
    assert.ok(stateBody.innerHTML.includes("&lt;img src=x onerror=alert(&quot;id&quot;)&gt;"));

    const peerTabBtn = elements.find((el) => el.textContent === "Peers");
    assert.ok(peerTabBtn && peerTabBtn.onclick);
    peerTabBtn.onclick();

    assert.ok(!stateBody.innerHTML.includes('<script>alert("peer")</script>'));
    assert.ok(stateBody.innerHTML.includes("&lt;script&gt;alert(&quot;peer&quot;)&lt;/script&gt;"));
    assert.ok(!stateBody.innerHTML.includes('<b onmouseover="alert(1)">Hacker</b>'));
    assert.ok(stateBody.innerHTML.includes("&lt;b onmouseover=&quot;alert(1)&quot;&gt;Hacker&lt;/b&gt;"));
    assert.ok(!stateBody.innerHTML.includes("expression(1)"));
    assert.ok(stateBody.innerHTML.includes("background: #38bdf8;"));

    dt.unmount();
  } finally {
    globalThis.document = originalDoc;
    globalThis.window = originalWin;
  }
});
