import { test } from "node:test";
import assert from "node:assert/strict";
import { Awareness } from "./awareness.js";

function fakeClient() {
  const handlers = {};
  return {
    clientId: "self",
    handlers,
    sent: [],
    on(event, cb) {
      (handlers[event] ||= []).push(cb);
      return this;
    },
    off(event, cb) {
      handlers[event] = (handlers[event] || []).filter((fn) => fn !== cb);
      return this;
    },
    presence(data) {
      this.sent.push(data);
    },
    emit(event, payload) {
      (handlers[event] || []).forEach((cb) => cb(payload));
    },
  };
}

test("awareness publishes identity and cursor state", () => {
  const sw = fakeClient();
  const awareness = new Awareness(sw, {
    user: { id: "u1", name: "Ada" },
    start: false,
  });

  awareness.setCursor({ index: 3, selectionEnd: 7 });
  assert.equal(sw.sent.at(-1).type, "awareness");
  assert.deepEqual(sw.sent.at(-1).state.cursor, { index: 3, selectionEnd: 7 });
  assert.equal(sw.sent.at(-1).state.user.name, "Ada");
  awareness.close();
});

test("awareness republishes its state on reconnect", () => {
  const sw = fakeClient();
  const awareness = new Awareness(sw, { user: { id: "u1" }, start: false });
  sw.emit("open");
  assert.equal(sw.sent.length, 1);
  assert.deepEqual(sw.sent[0].state.user, { id: "u1" });
  awareness.close();
});

test("awareness tracks and removes remote peers", () => {
  const sw = fakeClient();
  const awareness = new Awareness(sw, { user: { id: "u1" }, start: false });
  const events = [];
  awareness.on("join", (peer) => events.push(["join", peer.clientId]));
  awareness.on("remove", (id) => events.push(["remove", id]));

  sw.emit("presence", {
    clientId: "peer",
    data: { type: "awareness", state: { user: { id: "u2" }, cursor: { index: 1, selectionEnd: 1 } } },
  });
  assert.equal(awareness.get("peer").user.id, "u2");
  assert.equal(awareness.getStates().length, 2); // self + peer

  sw.emit("leave", { clientId: "peer" });
  assert.equal(awareness.get("peer"), undefined);
  assert.deepEqual(events, [["join", "peer"], ["remove", "peer"]]);
  awareness.close();
});

test("stale peers are pruned after the timeout", async () => {
  const sw = fakeClient();
  const awareness = new Awareness(sw, { user: { id: "u1" }, start: false, timeout: 20 });
  sw.emit("presence", { clientId: "gone", data: { type: "awareness", state: { user: { id: "u2" } } } });
  await new Promise((r) => setTimeout(r, 30));
  assert.equal(awareness.getStates().length, 1);
  awareness.close();
});
