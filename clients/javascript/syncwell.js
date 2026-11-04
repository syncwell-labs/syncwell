// Syncwell browser/Node client — zero dependencies, no build step.
//
// The client holds its own CRDT replica: writes apply locally first
// (optimistic, offline-capable) and converge with the server on reconnect. The
// UI should read from the local document and re-render on the "change" event.
//
// Usage:
//   import { Syncwell } from "/sdk/syncwell.js";
//   const sw = new Syncwell("ws://localhost:8080/ws", "my-room");
//   sw.on("change", live => render(live));   // live = { key: value, ... }
//   sw.connect();
//   sw.set("title", "Hello");                 // applies locally + syncs
//   sw.undo();
//
// Events: open, close, change, presence, leave, error, history.
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

function buildURL(baseURL, room, token, delta, tokenInQuery = false) {
  const url = new URL(baseURL, "http://syncwell.local");
  url.searchParams.set("room", room);
  if (token && tokenInQuery) url.searchParams.set("token", token);
  if (delta) url.searchParams.set("delta", "1");
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

function clone(value) {
  return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

function sameRegister(a, b) {
  if (a === undefined || b === undefined) return a === b;
  return JSON.stringify(a) === JSON.stringify(b);
}

function isThenable(value) {
  return value != null && typeof value.then === "function";
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
    this.baseURL = baseURL;
    this.room = room;
    this.token = opts.token;
    if (this.token != null && typeof this.token !== "string") {
      throw new TypeError("token must be a string");
    }
    this.tokenTransport = opts.tokenTransport ?? "subprotocol";
    if (this.tokenTransport !== "subprotocol" && this.tokenTransport !== "query") {
      throw new TypeError('tokenTransport must be "subprotocol" or "query"');
    }
    this.followOwner = opts.followOwner === true;
    this.fetch = opts.fetch || (typeof globalThis.fetch === "function" ? globalThis.fetch.bind(globalThis) : null);
    this._routePromise = null;
    this._routeGeneration = 0;
    this.url = buildURL(baseURL, room, opts.token, false, this.tokenTransport !== "subprotocol");
    this.clientId = opts.clientId || randomId();
    this.WS = opts.WebSocket || defaultWS();
    this.handlers = {};
    this.ws = null;
    this._ackTimer = null;
    this._reconnectTimer = null;
    this._helloTimer = null;
    this._reconnectDelay = 500;
    this._maxReconnectDelay = opts.maxReconnectDelay || 8000;
    this._closed = false;
    this._ready = false;
    this._pendingErrors = [];
    this.storage = defaultStorage(opts);
    this.storageKey = opts.storageKey || `syncwell:${room}:${this.clientId}`;
    this.epoch = 1;
    this._deltaEnabled = opts.delta !== false;
    this._delta = false;
    this._dirty = new Set();
    this._sentDirty = new Set();
    this._sentRegisters = new Map();
    this._historyLimit = opts.historyLimit ?? 100;
    this._undo = [];
    this._redo = [];
    this._batchDepth = 0;
    this._batchOps = [];
    this._batchChanged = false;
    this._replaying = false;
    this._saveChain = null;

    // The local CRDT replica. node id = clientId so its writes are globally
    // ordered against every other replica.
    this.clock = new Clock(this.clientId);
    this.doc = new LWWMap();
    const loaded = this._loadLocal();
    this.ready = isThenable(loaded) ? loaded : Promise.resolve();
    this._saveChain = this.ready;
  }

  on(event, cb) {
    (this.handlers[event] ||= []).push(cb);
    if (event === "error" && this._pendingErrors.length) {
      for (const err of this._pendingErrors.splice(0)) cb(err);
    }
    return this;
  }
  off(event, cb) {
    if (!cb) {
      delete this.handlers[event];
      return this;
    }
    this.handlers[event] = (this.handlers[event] || []).filter((fn) => fn !== cb);
    return this;
  }
  _emit(event, payload) {
    (this.handlers[event] || []).forEach((cb) => cb(payload));
  }
  _changed() {
    if (this._batchDepth > 0) {
      this._batchChanged = true;
      return;
    }
    this._emit("change", this.doc.live());
  }
  _historyChanged() {
    this._emit("history", { canUndo: this.canUndo(), canRedo: this.canRedo() });
  }

  connect() {
    if (!this.WS) throw new SyncwellError("NO_WEBSOCKET", "no WebSocket implementation available");
    if (this.ws && (this.ws.readyState === 0 || this.ws.readyState === 1)) return this;
    if (this._reconnectTimer) {
      clearTimeout(this._reconnectTimer);
      this._reconnectTimer = null;
    }
    this._closed = false;
    if (this.followOwner) {
      if (!this.fetch) throw new SyncwellError("NO_FETCH", "followOwner requires fetch support");
      if (this._routePromise) return this;
      const generation = ++this._routeGeneration;
      this._routePromise = this._resolveOwnerURL()
        .then((url) => {
          if (generation !== this._routeGeneration || this._closed) return;
          this._routePromise = null;
          this._connectSocket(url);
        })
        .catch((err) => {
          if (generation !== this._routeGeneration || this._closed) return;
          this._routePromise = null;
          this._emitError("ROUTE_FAILED", "could not resolve room owner", err);
          this._scheduleReconnect();
        });
      return this;
    }
    return this._connectSocket(this.baseURL);
  }

  async _resolveOwnerURL() {
    const base = new URL(this.baseURL, typeof location !== "undefined" ? location.href : "http://syncwell.local");
    const route = new URL(base.origin);
    route.protocol = base.protocol === "wss:" ? "https:" : "http:";
    route.pathname = "/route";
    route.searchParams.set("room", this.room);
    const response = await this.fetch(route.toString(), { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`route endpoint returned HTTP ${response.status}`);
    const data = await response.json();
    if (!data || typeof data.url !== "string") throw new Error("route response has no WebSocket URL");
    const ownerURL = new URL(data.url, route);
    if (ownerURL.protocol !== "ws:" && ownerURL.protocol !== "wss:") throw new Error("route response URL must use ws or wss");
    return ownerURL.toString();
  }

  _connectSocket(baseURL) {
    if (this.ws && (this.ws.readyState === 0 || this.ws.readyState === 1)) return this;
    this._closed = false;
    this._ready = false;
    this._delta = this._deltaEnabled && Object.keys(this._vector()).length > 0;
    this.url = buildURL(baseURL, this.room, this.token, this._delta, this.tokenTransport !== "subprotocol");
    const protocols = this.tokenTransport === "subprotocol" && this.token
      ? ["syncwell", "syncwell.token." + this.token]
      : undefined;
    const ws = protocols ? new this.WS(this.url, protocols) : new this.WS(this.url);
    this.ws = ws;
    this.ws.onopen = async () => {
      if (this.ws !== ws) return;
      this._reconnectDelay = 500;
      this._emit("open");
      if (this._delta) {
        this._sendHello();
      } else if (this.doc.entries.size > 0) {
        // Non-delta mode: wait for local state to load, then send ONLY
        // dirty (locally authored) writes as merge. Sending the full
        // snapshot would include remote registers from other nodes, causing
        // node ID mismatch on the server.
        await this.ready;
        if (this.ws !== ws) return;
        const dirtyState = this._dirtyState();
        if (Object.keys(dirtyState).length > 0) {
          this._sendRaw({ type: "merge", state: dirtyState });
        }
      }
    };
    this.ws.onclose = () => {
      if (this.ws !== ws) return;
      clearInterval(this._ackTimer);
      clearTimeout(this._helloTimer);
      this._ackTimer = null;
      this._helloTimer = null;
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
    this._routeGeneration++;
    this._routePromise = null;
    clearInterval(this._ackTimer);
    clearTimeout(this._reconnectTimer);
    clearTimeout(this._helloTimer);
    this._ackTimer = null;
    this._reconnectTimer = null;
    this._helloTimer = null;
    this._ready = false;
    if (this.ws) this.ws.close();
  }

  _connected() {
    return this.ws && this.ws.readyState === 1; // OPEN
  }
  _sendRaw(obj) {
    if (this._connected()) {
      this.ws.send(JSON.stringify({ ...obj, protocol: protocolVersion, epoch: this.epoch }));
    }
  }
  _send(obj) {
    if (this._ready) this._sendRaw(obj);
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

  _vector() {
    const out = {};
    for (const [key, reg] of this.doc.entries) out[key] = reg.t;
    return out;
  }
  _dirtyState() {
    this._pruneDirty();
    const out = {};
    for (const key of this._dirty) {
      const reg = this.doc.entries.get(key);
      if (reg) out[key] = reg;
    }
    return out;
  }
  _pruneDirty() {
    for (const key of this._dirty) {
      const reg = this.doc.entries.get(key);
      if (!reg || reg.t?.n !== this.clientId) this._dirty.delete(key);
    }
  }
  _sendHello() {
    const vector = this._vector();
    const state = this._dirtyState();
    this._sentDirty = new Set(Object.keys(state));
    this._sentRegisters = new Map(Object.entries(state).map(([k, v]) => [k, clone(v)]));
    this._sendRaw({ type: "hello", vector, state });
    // Older servers may not answer `hello`; fall back to the classic merge
    // handshake with only our dirty writes so a version skew can never strand
    // the client offline.
    clearTimeout(this._helloTimer);
    this._helloTimer = setTimeout(() => {
      if (this._ready || this.ws?.readyState !== 1) return;
      const dirtyState = this._dirtyState();
      if (Object.keys(dirtyState).length > 0) {
        this._sendRaw({ type: "merge", state: dirtyState });
      }
    }, 2000);
  }
  _clearAcknowledgedDirty() {
    for (const key of this._sentDirty) {
      const sent = this._sentRegisters.get(key);
      const cur = this.doc.entries.get(key);
      if (sameRegister(cur, sent)) this._dirty.delete(key);
    }
    this._sentDirty.clear();
    this._sentRegisters.clear();
  }
  _startReady() {
    if (this._ready) return;
    this._ready = true;
    clearTimeout(this._helloTimer);
    this._helloTimer = null;
    this._ack();
    if (this._pendingPresence !== null) {
      const data = this._pendingPresence;
      this._pendingPresence = null;
      this._sendRaw({ type: "presence", data });
    }
    clearInterval(this._ackTimer);
    this._ackTimer = setInterval(() => this._ack(), 3000);
  }

  _loadLocal() {
    if (!this.storage) return;
    try {
      const raw = this.storage.getItem(this.storageKey);
      if (isThenable(raw)) {
        return Promise.resolve(raw)
          .then((value) => this._applySaved(value))
          .catch((err) => this._emitError("STORAGE_READ", "could not load local Syncwell state", err));
      }
      this._applySaved(raw);
    } catch (err) {
      this._emitError("STORAGE_READ", "could not load local Syncwell state", err);
    }
  }
  _applySaved(raw) {
    if (!raw) return;
    const saved = typeof raw === "string" ? JSON.parse(raw) : raw;
    if (saved?.version !== 1 || !saved.state) return;
    this.doc.mergeState(saved.state);
    this.clock.counter = Math.max(this.clock.counter, Number(saved.clock) || 0);
    this.epoch = Math.max(1, Number(saved.epoch) || 1);
    for (const k of Object.keys(saved.state)) this.clock.observe(saved.state[k]?.t);
    if (Array.isArray(saved.dirty)) this._dirty = new Set(saved.dirty.filter((k) => this.doc.entries.has(k)));
    if (Array.isArray(saved.undo)) this._undo = saved.undo;
    if (Array.isArray(saved.redo)) this._redo = saved.redo;
  }
  _saveLocal() {
    if (!this.storage) return;
    const payload = JSON.stringify({
      version: 1,
      clock: this.clock.counter,
      epoch: this.epoch,
      state: this.doc.snapshot(),
      dirty: [...this._dirty],
      undo: this._undo,
      redo: this._redo,
    });
    try {
      const result = this.storage.setItem(this.storageKey, payload);
      if (isThenable(result)) {
        this._saveChain = Promise.resolve(result).catch((err) =>
          this._emitError("STORAGE_WRITE", "could not save local Syncwell state", err),
        );
      }
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
        if (!this._ready) this._dirty.clear();
        else this._pruneDirty();
        this._saveLocal();
        if (!this._ready) this._startReady();
        this._changed();
        break;
      case "delta":
        this.epoch = Math.max(1, Number(msg.epoch) || this.epoch);
        for (const k of Object.keys(msg.state || {})) this.clock.observe(msg.state[k].t);
        this.doc.mergeState(msg.state || {});
        this._clearAcknowledgedDirty();
        this._pruneDirty();
        this._saveLocal();
        this._startReady();
        this._changed();
        break;
      case "op":
        if (msg.epoch && msg.epoch !== this.epoch) {
          this._emitError("STALE_EPOCH", `stale epoch ${msg.epoch}`);
          if (this.ws) this.ws.close();
          return;
        }
        this.clock.observe(msg.register?.t);
        this.doc.mergeRegister(msg.key, msg.register);
        this._pruneDirty();
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

  _record(op) {
    if (this._replaying) return;
    if (this._batchDepth > 0) {
      this._batchOps.push(op);
      return;
    }
    this._pushUndo([op]);
  }
  _pushUndo(group) {
    if (!group.length) return;
    this._undo.push(group);
    if (this._undo.length > this._historyLimit) this._undo.shift();
    this._redo = [];
    this._historyChanged();
  }

  /** Run several writes as one change event and one undo step. */
  batch(fn) {
    if (this._batchDepth === 0) this._batchOps = [];
    this._batchDepth += 1;
    try {
      return fn();
    } finally {
      this._batchDepth -= 1;
      if (this._batchDepth === 0) {
        const ops = this._batchOps;
        this._batchOps = [];
        this._pushUndo(ops);
        if (this._batchChanged) {
          this._batchChanged = false;
          this._changed();
        }
      }
    }
  }

  _localWrite(key, register, record = true) {
    // Apply locally first (optimistic / offline), notify the UI, then sync. If
    // offline, the edit lives in the local CRDT and is shipped on reconnect.
    const before = clone(this.doc.entries.get(key));
    this.doc.mergeRegister(key, register);
    if (record) this._record({ key, before, after: clone(register) });
    this._dirty.add(key);
    this._saveLocal();
    this._changed();
    this._send({ type: "op", key, register });
  }

  _applyHistory(key, reg) {
    let next;
    if (reg === undefined) {
      next = { t: this.clock.tick(), d: true };
    } else {
      next = { ...clone(reg), t: this.clock.tick() };
      if (!next.d) delete next.d;
    }
    this._replaying = true;
    try {
      this._localWrite(key, next, false);
    } finally {
      this._replaying = false;
    }
  }

  undo() {
    while (this._undo.length) {
      const group = this._undo.pop();
      const redo = [];
      for (let i = group.length - 1; i >= 0; i--) {
        const op = group[i];
        const cur = this.doc.entries.get(op.key);
        // Only undo an edit this replica still owns; if a remote write landed
        // afterwards, undoing would silently discard it.
        if (!this._owns(cur, op.after)) continue;
        this._applyHistory(op.key, op.before);
        redo.unshift({ key: op.key, before: op.after, after: op.before });
      }
      if (redo.length) {
        this._redo.push(redo);
        this._historyChanged();
        return true;
      }
    }
    this._historyChanged();
    return false;
  }

  redo() {
    while (this._redo.length) {
      const group = this._redo.pop();
      const undo = [];
      for (const op of group) {
        const cur = this.doc.entries.get(op.key);
        if (!this._owns(cur, op.after)) continue;
        this._applyHistory(op.key, op.before);
        undo.push({ key: op.key, before: op.after, after: op.before });
      }
      if (undo.length) {
        this._undo.push(undo);
        this._historyChanged();
        return true;
      }
    }
    this._historyChanged();
    return false;
  }

  _owns(cur, want) {
    if (!cur) return want === undefined;
    // Undo/redo writes get fresh timestamps, so compare content (and author),
    // not the exact register, when deciding whether the stack entry still
    // describes what is on screen.
    if (cur.t?.n !== this.clientId) return false;
    if (want === undefined) return Boolean(cur.d);
    return Boolean(cur.d) === Boolean(want.d) && JSON.stringify(cur.v) === JSON.stringify(want.v);
  }

  canUndo() {
    return this._undo.length > 0;
  }
  canRedo() {
    return this._redo.length > 0;
  }
  clearHistory() {
    this._undo = [];
    this._redo = [];
    this._historyChanged();
  }

  set(key, value) {
    this._localWrite(key, { v: value, t: this.clock.tick() });
  }
  delete(key) {
    this._localWrite(key, { t: this.clock.tick(), d: true });
  }

  // Ephemeral, non-persisted broadcast (cursors, selections, who's-online).
  presence(data) {
    if (!this._ready) {
      // Presence is ephemeral, so keep only the latest value until the first
      // snapshot/delta makes the socket writable. Without this, a caller that
      // publishes on `open` loses its initial cursor/identity.
      this._pendingPresence = data;
      return;
    }
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
