import { test } from "node:test";
import assert from "node:assert/strict";
import { Syncwell, SyncwellError, protocolVersion } from "./syncwell.js";

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 3;
  static last = null;
  static instances = [];

  constructor(url, protocols) {
    this.url = url;
    this.protocols = protocols;
    this.readyState = FakeWebSocket.OPEN;
    this.sent = [];
    FakeWebSocket.last = this;
    FakeWebSocket.instances.push(this);
  }

  send(message) {
    this.sent.push(message);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.();
  }
}

class MemoryStorage {
  constructor() {
    this.items = new Map();
  }
  getItem(key) {
    return this.items.has(key) ? this.items.get(key) : null;
  }
  setItem(key, value) {
    this.items.set(key, value);
  }
}

test("constructor appends room and token (query mode) without breaking existing query params", () => {
  const sw = new Syncwell("ws://localhost:8080/ws?debug=1", "team/a", {
    token: "exp.sig",
    tokenTransport: "query",
    WebSocket: FakeWebSocket,
  });

  sw.connect();

  const url = new URL(FakeWebSocket.last.url);
  assert.equal(url.searchParams.get("debug"), "1");
  assert.equal(url.searchParams.get("room"), "team/a");
  assert.equal(url.searchParams.get("token"), "exp.sig");
  sw.close();
});

test("default subprotocol transport keeps the token out of the URL", () => {
  const sw = new Syncwell("ws://localhost:8080/ws", "team/a", {
    token: "exp.sig",
    WebSocket: FakeWebSocket,
  });

  sw.connect();

  const url = new URL(FakeWebSocket.last.url);
  assert.equal(url.searchParams.get("token"), null);
  assert.deepEqual(FakeWebSocket.last.protocols, ["syncwell", "syncwell.token.exp.sig"]);
  sw.close();
});

test("followOwner resolves the room route before opening the WebSocket", async () => {
  let routeURL;
  const sw = new Syncwell("ws://seed.example/ws", "room/one", {
    followOwner: true,
    token: "secret-token",
    WebSocket: FakeWebSocket,
    fetch: async (url) => {
      routeURL = new URL(url);
      return { ok: true, status: 200, json: async () => ({ node: "node-b", url: "wss://owner.example/ws" }) };
    },
  });
  sw.connect();
  await sw._routePromise;
  assert.equal(routeURL.pathname, "/route");
  assert.equal(routeURL.searchParams.get("room"), "room/one");
  const wsURL = new URL(FakeWebSocket.last.url);
  assert.equal(wsURL.host, "owner.example");
  assert.equal(wsURL.searchParams.get("room"), "room/one");
  assert.equal(wsURL.searchParams.has("token"), false);
  sw.close();
});

test("malformed inbound JSON is surfaced as an error event", () => {
  const sw = new Syncwell("ws://localhost:8080/ws", "room", { WebSocket: FakeWebSocket });
  let seen = null;
  sw.on("error", (err) => { seen = err; });
  sw.connect();

  FakeWebSocket.last.onmessage({ data: "not-json" });

  assert.ok(seen instanceof SyncwellError);
  assert.equal(seen.code, "BAD_FRAME");
  assert.ok(seen.cause instanceof SyntaxError);
  sw.close();
});

test("server error frames are surfaced as error events", () => {
  const sw = new Syncwell("ws://localhost:8080/ws", "room", { WebSocket: FakeWebSocket });
  let seen = null;
  sw.on("error", (err) => { seen = err; });
  sw.connect();

  FakeWebSocket.last.onmessage({ data: JSON.stringify({ type: "error", data: "persistence failed" }) });

  assert.ok(seen instanceof SyncwellError);
  assert.equal(seen.code, "SERVER_ERROR");
  assert.equal(seen.message, "persistence failed");
  sw.close();
});

test("outbound frames carry the current protocol version", () => {
  const sw = new Syncwell("ws://localhost:8080/ws", "room", { WebSocket: FakeWebSocket });
  sw.connect();

  // Writes are intentionally held until the room handshake (snapshot) completes;
  // feed one so the subsequent `op` is actually put on the wire.
  FakeWebSocket.last.onmessage({
    data: JSON.stringify({ protocol: protocolVersion, type: "snapshot", epoch: 1, state: {} }),
  });
  sw.set("title", "hello");

  const sent = FakeWebSocket.last.sent.map((msg) => JSON.parse(msg));
  const op = sent.find((msg) => msg.type === "op");
  assert.ok(op, "expected an outbound op frame");
  assert.equal(op.protocol, protocolVersion);
  sw.close();
});

test("unsupported inbound protocol is surfaced as an error event", () => {
  const sw = new Syncwell("ws://localhost:8080/ws", "room", { WebSocket: FakeWebSocket });
  let seen = null;
  sw.on("error", (err) => { seen = err; });
  sw.connect();

  FakeWebSocket.last.onmessage({ data: JSON.stringify({ protocol: 999, type: "snapshot", state: {} }) });

  assert.ok(seen instanceof SyncwellError);
  assert.equal(seen.code, "UNSUPPORTED_PROTOCOL");
  sw.close();
});

test("local storage persists document and logical clock across instances", () => {
  const storage = new MemoryStorage();
  const opts = {
    clientId: "stable-client",
    storage,
    WebSocket: FakeWebSocket,
  };

  const first = new Syncwell("ws://localhost:8080/ws", "room", opts);
  first.set("title", "offline draft");
  assert.equal(first.clock.counter, 1);

  const second = new Syncwell("ws://localhost:8080/ws", "room", opts);
  assert.equal(second.get("title"), "offline draft");
  assert.equal(second.clock.counter, 1);
  second.set("title", "next draft");
  assert.equal(second.clock.counter, 2);
});

test("connect is idempotent while a socket is already open", () => {
  FakeWebSocket.instances = [];
  const sw = new Syncwell("ws://localhost:8080/ws", "room", { WebSocket: FakeWebSocket });

  sw.connect();
  sw.connect();

  assert.equal(FakeWebSocket.instances.length, 1);
  sw.close();
});
