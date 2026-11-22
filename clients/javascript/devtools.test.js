import { test } from "node:test";
import assert from "node:assert/strict";
import { mountDevTools } from "./devtools.js";

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
