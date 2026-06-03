// Syncwell browser/Node client — zero dependencies, no build step.
//
// As of v0.2 the client holds its own CRDT replica: writes apply locally first
// (optimistic, offline-capable) and converge with the server on reconnect. The
// UI should read from the local document and re-render on the "change" event.
//
// Usage:
//   import { Syncwell } from "/sdk/syncwell.js";
//   const sw = new Syncwell("ws://localhost:8080/ws", "my-room");
//   sw.on("change", live => render(live));   // live = { key: value, ... }
//   sw.connect();
//   sw.set("title", "Hello");                 // applies locally + syncs
//
// Events: open, close, change, presence, leave.
import { LWWMap, Clock } from "./crdt.js";

// Allow injecting a WebSocket implementation (Node tests pass `ws`); in the
// browser the global is used automatically.
function defaultWS() {
  return typeof WebSocket !== "undefined" ? WebSocket : null;
}

function randomId() {
  // Browser/Node both expose crypto; fall back to a cheap id if absent.
  try {
    return "c-" + crypto.randomUUID().slice(0, 8);
  } catch {
    return "c-" + Math.floor(Math.random() * 1e9).toString(36);
  }
}

export class Syncwell {
  constructor(baseURL, room = "default", opts = {}) {
    this.url = `${baseURL}?room=${encodeURIComponent(room)}`;
    this.clientId = opts.clientId || randomId();
    this.WS = opts.WebSocket || defaultWS();
    this.handlers = {};
    this.ws = null;
    this._reconnectDelay = 500;
    this._closed = false;

    // The local CRDT replica. node id = clientId so its writes are globally
    // ordered against every other replica.
    this.clock = new Clock(this.clientId);
    this.doc = new LWWMap();
  }

  on(event, cb) {
    (this.handlers[event] ||= []).push(cb);
    return this;
  }
  _emit(event, payload) {
    (this.handlers[event] || []).forEach((cb) => cb(payload));
  }
  _changed() {
    this._emit("change", this.doc.live());
  }

  connect() {
    if (!this.WS) throw new Error("no WebSocket implementation available");
    this._closed = false;
    this.ws = new this.WS(this.url);
    this.ws.onopen = () => {
      this._reconnectDelay = 500;
      // Ship our full local state so any edits made while offline propagate.
      // (Empty for a fresh client — the server still replies with a snapshot
      // via the join handler, so we skip the redundant round-trip.)
      const state = this.doc.snapshot();
      if (Object.keys(state).length) this._send({ type: "merge", state });
      this._ack();
      // Heartbeat: advertise our clock so the server's GC low-water mark can
      // advance even when we're idle.
      this._ackTimer = setInterval(() => this._ack(), 3000);
      this._emit("open");
    };
    this.ws.onclose = () => {
      clearInterval(this._ackTimer);
      this._emit("close");
      if (this._closed) return;
      setTimeout(() => this.connect(), this._reconnectDelay);
      this._reconnectDelay = Math.min(this._reconnectDelay * 2, 8000);
    };
    this.ws.onmessage = (e) => this._recv(JSON.parse(e.data));
    this.ws.onerror = () => {}; // close handler drives reconnect
    return this;
  }

  close() {
    this._closed = true;
    clearInterval(this._ackTimer);
    if (this.ws) this.ws.close();
  }

  _connected() {
    return this.ws && this.ws.readyState === 1; // OPEN
  }
  _send(obj) {
    if (this._connected()) this.ws.send(JSON.stringify(obj));
  }
  _ack() {
    this._send({ type: "ack", clock: this.clock.counter });
  }

  _recv(msg) {
    switch (msg.type) {
      case "snapshot":
        for (const k of Object.keys(msg.state || {})) this.clock.observe(msg.state[k].t);
        this.doc.mergeState(msg.state || {});
        this._changed();
        break;
      case "op":
        this.clock.observe(msg.register?.t);
        this.doc.mergeRegister(msg.key, msg.register);
        this._changed();
        break;
      case "presence":
        this._emit("presence", msg);
        break;
      case "leave":
        this._emit("leave", msg);
        break;
    }
  }

  _localWrite(key, register) {
    // Apply locally first (optimistic / offline), notify the UI, then sync. If
    // offline, the edit lives in the local CRDT and is shipped on reconnect.
    this.doc.mergeRegister(key, register);
    this._changed();
    this._send({ type: "op", key, register });
  }

  set(key, value) {
    this._localWrite(key, { v: value, t: this.clock.tick() });
  }
  delete(key) {
    this._localWrite(key, { t: this.clock.tick(), d: true });
  }

  // Ephemeral, non-persisted broadcast (cursors, selections, who's-online).
  presence(data) {
    this._send({ type: "presence", data });
  }

  // Read APIs — the UI reads from the local replica.
  get(key) {
    return this.doc.get(key);
  }
  live() {
    return this.doc.live();
  }
}
