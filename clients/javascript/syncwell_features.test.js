import { test } from "node:test";
import assert from "node:assert/strict";
import { Syncwell, protocolVersion } from "./syncwell.js";

class FakeWebSocket {
  static OPEN = 1;
  static last = null;
  static instances = [];
  constructor(url) {
    this.url = url;
    this.readyState = FakeWebSocket.OPEN;
    this.sent = [];
    FakeWebSocket.last = this;
    FakeWebSocket.instances.push(this);
  }
  send(message) {
    this.sent.push(message);
  }
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
}

class MemoryStorage {
  constructor(initial = {}) {
    this.items = new Map(Object.entries(initial));
  }
  getItem(key) {
    return this.items.has(key) ? this.items.get(key) : null;
  }
  setItem(key, value) {
    this.items.set(key, value);
  }
}

class AsyncStorage {
  constructor(initial = {}) {
    this.items = new Map(Object.entries(initial));
  }
  async getItem(key) {
    await new Promise((r) => setImmediate(r));
    return this.items.has(key) ? this.items.get(key) : null;
  }
  async setItem(key, value) {
    await new Promise((r) => setImmediate(r));
    this.items.set(key, value);
  }
}

test("batch groups writes into one undo step", () => {
  const sw = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "A" });
  sw.batch(() => {
    sw.set("a", 1);
    sw.set("b", 2);
  });
  assert.equal(sw.get("a"), 1);
  assert.equal(sw.get("b"), 2);
  assert.equal(sw.canUndo(), true);
  sw.undo();
  assert.equal(sw.get("a"), undefined);
  assert.equal(sw.get("b"), undefined);
  sw.redo();
  assert.equal(sw.get("a"), 1);
  assert.equal(sw.get("b"), 2);
  sw.close();
});

test("presence published before the first snapshot is sent once ready", () => {
  const sw = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "A" });
  sw.connect();
  sw.presence({ cursor: { index: 2 } });
  assert.equal(FakeWebSocket.last.sent.length, 0);
  sw._recv({ type: "snapshot", state: {}, epoch: 1 });
  const frames = FakeWebSocket.last.sent.map((raw) => JSON.parse(raw));
  const presence = frames.find((frame) => frame.type === "presence");
  assert.deepEqual(presence?.data, { cursor: { index: 2 } });
  sw.close();
});

test("undo does not clobber a newer remote write", () => {
  const sw = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "A" });
  sw.set("k", "local");
  // A remote op wins after our local write.
  sw.doc.mergeRegister("k", { v: "remote", t: { c: 100, n: "B" } });
  assert.equal(sw.undo(), false);
  assert.equal(sw.get("k"), "remote");
  sw.close();
});

test("async storage adapters are awaited through sw.ready", async () => {
  const storage = new AsyncStorage();
  const first = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "stable", storage });
  await first.ready;
  first.set("title", "async persisted");
  await first._saveChain;

  const second = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "stable", storage });
  await second.ready;
  assert.equal(second.get("title"), "async persisted");
  assert.equal(second.clock.counter, first.clock.counter);
  first.close();
  second.close();
});

test("reconnect with local state uses state-vector delta sync", async () => {
  const storage = new MemoryStorage();
  const first = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "A", storage });
  await first.ready;
  first.set("k", "offline");
  await first._saveChain;

  FakeWebSocket.instances = [];
  const second = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "A", storage });
  await second.ready;
  second.connect();
  const ws = FakeWebSocket.last;
  assert.match(ws.url, /delta=1/);
  ws.onopen();

  const hello = ws.sent.map((m) => JSON.parse(m)).find((m) => m.type === "hello");
  assert.ok(hello, "expected a hello frame");
  assert.deepEqual(hello.vector.k, { c: 1, n: "A" });
  assert.equal(hello.state.k.v, "offline");

  ws.onmessage({
    data: JSON.stringify({
      protocol: protocolVersion,
      type: "delta",
      epoch: 1,
      state: { k: { v: "offline", t: { c: 1, n: "A" } }, remote: { v: "x", t: { c: 2, n: "B" } } },
    }),
  });
  assert.equal(second._ready, true);
  assert.equal(second.get("remote"), "x");
  second.close();
});

test("storage persists the dirty key set for delta resume", async () => {
  const storage = new MemoryStorage();
  const first = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "A", storage });
  await first.ready;
  first.set("offline", 42);
  await first._saveChain;
  const saved = JSON.parse(storage.getItem(first.storageKey));
  assert.deepEqual(saved.dirty, ["offline"]);
  first.close();
});

test("remote overwrite prunes a stale dirty key before reconnect", () => {
  const sw = new Syncwell("ws://localhost/ws", "room", { WebSocket: FakeWebSocket, clientId: "A" });
  sw.connect();
  sw._recv({ type: "snapshot", state: {}, epoch: 1 });
  sw.set("k", "local");
  assert.equal(sw._dirty.has("k"), true);

  sw._recv({
    type: "op",
    epoch: 1,
    key: "k",
    register: { v: "remote", t: { c: 2, n: "B" } },
  });
  assert.equal(sw.get("k"), "remote");
  assert.equal(sw._dirty.has("k"), false);

  sw.set("j", "newer-local");
  sw._recv({
    type: "op",
    epoch: 1,
    key: "j",
    register: { v: "older-remote", t: { c: 0, n: "B" } },
  });
  assert.equal(sw.get("j"), "newer-local");
  assert.equal(sw._dirty.has("j"), true);
  sw.close();
});
