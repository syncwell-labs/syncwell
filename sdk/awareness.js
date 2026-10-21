// Typed ephemeral awareness: cursors, selections, hover state, and identity.
//
// Presence frames are relayed by the server but never persisted. Awareness
// adds a heartbeat and a stale-peer timeout on top, so a crashed tab eventually
// disappears instead of leaving a ghost cursor forever.
//
// Usage:
//   const awareness = new Awareness(sw, { user: { id: "u1", name: "Ada" } });
//   awareness.setCursor({ index: 4, selectionEnd: 4 });
//   awareness.on("update", (states) => renderCursors(states));
//   awareness.getStates(); // includes `self: true` for the local state
import { SyncwellError } from "./syncwell.js";

const DEFAULT_HEARTBEAT = 10_000;
const DEFAULT_TIMEOUT = 30_000;

function normaliseCursor(cursor) {
  if (!cursor || typeof cursor !== "object") return null;
  const index = Number(cursor.index);
  if (!Number.isFinite(index)) return null;
  const end = Number(cursor.selectionEnd ?? cursor.end ?? index);
  return {
    index: Math.max(0, index),
    selectionEnd: Number.isFinite(end) ? Math.max(0, end) : Math.max(0, index),
  };
}

export class Awareness {
  constructor(syncwell, opts = {}) {
    if (!syncwell) throw new SyncwellError("NO_CLIENT", "Awareness requires a Syncwell client");
    this.sw = syncwell;
    this.clientId = opts.clientId || syncwell.clientId;
    this.user = opts.user || { id: this.clientId, name: opts.name || this.clientId };
    this.color = opts.color || hueColor(this.clientId);
    this.local = { user: this.user, color: this.color, ...(opts.state || {}) };
    this.remote = new Map();
    this.handlers = {};
    this.heartbeatMs = opts.heartbeat ?? DEFAULT_HEARTBEAT;
    this.timeoutMs = opts.timeout ?? DEFAULT_TIMEOUT;
    this._timer = null;
    this._onPresence = (event) => this._receive(event);
    this._onLeave = (event) => {
      if (this.remote.delete(event.clientId)) this._emit("remove", event.clientId);
    };
    this._onOpen = () => this._send();
    syncwell.on("presence", this._onPresence);
    syncwell.on("leave", this._onLeave);
    syncwell.on("open", this._onOpen);
    if (opts.start !== false) this.start();
  }

  start() {
    if (this._timer) return this;
    this._blockedReset = false;
    this._send();
    this._timer = setInterval(() => {
      this._prune();
      this._send();
    }, this.heartbeatMs);
    this._timer.unref?.();
    return this;
  }

  stop() {
    if (this._timer) clearInterval(this._timer);
    this._timer = null;
    return this;
  }

  on(event, cb) {
    (this.handlers[event] ||= []).push(cb);
    return this;
  }
  off(event, cb) {
    this.handlers[event] = (this.handlers[event] || []).filter((fn) => fn !== cb);
    return this;
  }
  _emit(event, payload) {
    (this.handlers[event] || []).forEach((cb) => cb(payload));
  }

  setUser(user) {
    this.user = user;
    this.local = { ...this.local, user };
    this._send();
    return this;
  }

  setColor(color) {
    this.color = color;
    this.local = { ...this.local, color };
    this._send();
    return this;
  }

  setCursor(cursor) {
    return this.update({ cursor: normaliseCursor(cursor) });
  }

  setSelection(index, selectionEnd = index) {
    return this.update({ cursor: normaliseCursor({ index, selectionEnd }) });
  }

  update(partial) {
    this.local = { ...this.local, ...partial };
    this._send();
    this._emit("update", this.getStates());
    return this;
  }

  get(clientId) {
    if (clientId === this.clientId) return this._selfState();
    return this.remote.get(clientId);
  }

  getStates() {
    this._prune();
    return [this._selfState(), ...this.remote.values()];
  }

  _selfState() {
    return { clientId: this.clientId, self: true, ...this.local, lastSeen: Date.now() };
  }

  _send() {
    try {
      this.sw.presence({
        type: "awareness",
        state: {
          user: this.local.user,
          color: this.local.color,
          cursor: this.local.cursor || null,
          selection: this.local.selection || null,
          status: this.local.status,
        },
      });
    } catch (err) {
      this._emit("error", err);
    }
  }

  _receive(event) {
    const data = event?.data;
    if (!data || data.type !== "awareness" || !event.clientId) return;
    if (event.clientId === this.clientId) return;
    const prior = this.remote.get(event.clientId);
    const next = {
      clientId: event.clientId,
      self: false,
      ...(data.state || {}),
      lastSeen: Date.now(),
    };
    this.remote.set(event.clientId, next);
    this._emit("update", this.getStates());
    if (!prior) this._emit("join", next);
    else this._emit("change", next);
  }

  _prune() {
    const cutoff = Date.now() - this.timeoutMs;
    let changed = false;
    for (const [clientId, state] of this.remote) {
      if (state.lastSeen < cutoff) {
        this.remote.delete(clientId);
        changed = true;
        this._emit("remove", clientId);
      }
    }
    if (changed) this._emit("update", this.getStates());
  }

  close() {
    this.stop();
    this.sw.off("presence", this._onPresence);
    this.sw.off("leave", this._onLeave);
    this.sw.off("open", this._onOpen);
    this.remote.clear();
  }
}

function hueColor(seed) {
  let hash = 0;
  for (let i = 0; i < seed.length; i++) hash = (hash * 31 + seed.charCodeAt(i)) >>> 0;
  return `hsl(${hash % 360} 70% 55%)`;
}
