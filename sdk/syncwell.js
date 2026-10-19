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
// Events: open, close, change, presence, leave, error.
import { LWWMap, Clock } from "./crdt.js";

export const protocolVersion = 1;

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

function buildURL(baseURL, room, token) {
  const url = new URL(baseURL, "http://syncwell.local");
  url.searchParams.set("room", room);
  if (token) url.searchParams.set("token", token);
  const out = url.toString();
  return baseURL.startsWith("/") ? out.replace("http://syncwell.local", "") : out;
}

function defaultStorage(opts) {
  if (opts.storage) return opts.storage;
  if (!opts.persist) return null;
  try {
    return typeof localStorage !== "undefined" ? localStorage : null;
  } catch {
    return null;
  }
}

export class SyncwellError extends Error {
  constructor(code, message, cause) {
    super(message);
    this.name = "SyncwellError";
    this.code = code;
    this.cause = cause;
  }
}

export class Syncwell {
  constructor(baseURL, room = "default", opts = {}) {
    this.url = buildURL(baseURL, room, opts.token);
    this.clientId = opts.clientId || randomId();
    this.WS = opts.WebSocket || defaultWS();
    this.handlers = {};
    this.ws = null;
    this._ackTimer = null;
    this._reconnectTimer = null;
    this._reconnectDelay = 500;
    this._maxReconnectDelay = opts.maxReconnectDelay || 8000;
    this._closed = false;
    this._ready = false;
    this._pendingErrors = [];
    this.storage = defaultStorage(opts);
    this.storageKey = opts.storageKey || `syncwell:${room}:${this.clientId}`;
    this.epoch = 1;

    // The local CRDT replica. node id = clientId so its writes are globally
    // ordered against every other replica.
    this.clock = new Clock(this.clientId);
    this.doc = new LWWMap();
    this._loadLocal();
  }

  on(event, cb) {
    (this.handlers[event] ||= []).push(cb);
    if (event === "error" && this._pendingErrors.length) {
      for (const err of this._pendingErrors.splice(0)) cb(err);
    }
    return this;
  }
  _emit(event, payload) {
    (this.handlers[event] || []).forEach((cb) => cb(payload));
  }
  _changed() {
    this._emit("change", this.doc.live());
  }

  connect() {
    if (!this.WS) throw new SyncwellError("NO_WEBSOCKET", "no WebSocket implementation available");
    if (this.ws && (this.ws.readyState === 0 || this.ws.readyState === 1)) return this;
    if (this._reconnectTimer) {
      clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    this._closed = false;
    this._ready = false;
    const ws = new this.WS(this.url);
    this.ws = ws;
    this.ws.onopen = () => {
      if (this.ws !== ws) return;
      this._reconnectDelay = 500;
      this._emit("open");
    };
    this.ws.onclose = () => {
      if (this.ws !== ws) return;
      clearInterval(this._ackTimer);
      this._ackTimer = null;
      this._ready = false;
      this._emit("close");
      if (this._closed) return;
      this._scheduleReconnect();
    };
    this.ws.onmessage = (e) => {
      if (this.ws !== ws) return;
      try {
        this._recv(JSON.parse(e.data));
      } catch (err) {
        this._emitError("BAD_FRAME", "could not parse server frame", err);
      }
    };
    this.ws.onerror = () => {}; // close handler drives reconnect
    return this;
  }

  close() {
    this._closed = true;
    clearInterval(this._ackTimer);
    clearTimeout(this._reconnectTimer);
    this._ackTimer = null;
    this._reconnectTimer = null;
    this._ready = false;
    if (this.ws) this.ws.close();
  }

  _connected() {
    return this.ws && this.ws.readyState === 1; // OPEN
  }
  _send(obj) {
    if (this._connected() && this._ready) {
      this.ws.send(JSON.stringify({ ...obj, protocol: protocolVersion, epoch: this.epoch }));
    }
  }
  _ack() {
    this._send({ type: "ack", clock: this.clock.counter });
  }
  _scheduleReconnect() {
    if (this._reconnectTimer) return;
    const delay = this._reconnectDelay;
    this._reconnectDelay = Math.min(this._reconnectDelay * 2, this._maxReconnectDelay);
    this._reconnectTimer = setTimeout(() => {
      this._reconnectTimer = null;
      this.connect();
    }, delay);
  }
  _emitError(code, message, cause) {
    const err = cause instanceof SyncwellError ? cause : new SyncwellError(code, message, cause);
    if (this.handlers.error?.length) this._emit("error", err);
    else this._pendingErrors.push(err);
  }
  _loadLocal() {
    if (!this.storage) return;
    try {
      const raw = this.storage.getItem(this.storageKey);
      if (!raw) return;
      const saved = JSON.parse(raw);
      if (saved?.version !== 1 || !saved.state) return;
      this.doc.mergeState(saved.state);
      this.clock.counter = Math.max(this.clock.counter, Number(saved.clock) || 0);
      this.epoch = Math.max(1, Number(saved.epoch) || 1);
      for (const k of Object.keys(saved.state)) this.clock.observe(saved.state[k]?.t);
    } catch (err) {
      this._emitError("STORAGE_READ", "could not load local Syncwell state", err);
    }
  }
  _saveLocal() {
    if (!this.storage) return;
    try {
      this.storage.setItem(this.storageKey, JSON.stringify({
        version: 1,
        clock: this.clock.counter,
        epoch: this.epoch,
        state: this.doc.snapshot(),
      }));
    } catch (err) {
      this._emitError("STORAGE_WRITE", "could not save local Syncwell state", err);
    }
  }

  _recv(msg) {
    if (msg.protocol && msg.protocol !== protocolVersion) {
      this._emitError("UNSUPPORTED_PROTOCOL", `unsupported protocol ${msg.protocol}`);
      return;
    }
    switch (msg.type) {
      case "snapshot":
        this.epoch = Math.max(1, Number(msg.epoch) || this.epoch);
        for (const k of Object.keys(msg.state || {})) this.clock.observe(msg.state[k].t);
        this.doc.mergeState(msg.state || {});
        this._saveLocal();
        this._changed();
        if (!this._ready) {
          this._ready = true;
          const state = this.doc.snapshot();
          if (Object.keys(state).length) {
            this.ws?.send(JSON.stringify({
              type: "merge",
              state,
              protocol: protocolVersion,
              epoch: this.epoch,
            }));
          }
          this._ack();
          this._ackTimer = setInterval(() => this._ack(), 3000);
        }
        break;
      case "op":
        if (msg.epoch && msg.epoch !== this.epoch) {
          this._emitError("STALE_EPOCH", `stale epoch ${msg.epoch}`);
          if (this.ws) this.ws.close();
          return;
        }
        this.clock.observe(msg.register?.t);
        this.doc.mergeRegister(msg.key, msg.register);
        this._saveLocal();
        this._changed();
        break;
      case "presence":
        this._emit("presence", msg);
        break;
      case "leave":
        this._emit("leave", msg);
        break;
      case "error":
        if (typeof msg.data === "string" && msg.data.includes("stale epoch")) {
          this._emitError("STALE_EPOCH", msg.data);
          if (this.ws) this.ws.close();
          return;
        }
        this._emitError("SERVER_ERROR", String(msg.data || "server error"));
        break;
    }
  }

  _localWrite(key, register) {
    // Apply locally first (optimistic / offline), notify the UI, then sync. If
    // offline, the edit lives in the local CRDT and is shipped on reconnect.
    this.doc.mergeRegister(key, register);
    this._saveLocal();
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
