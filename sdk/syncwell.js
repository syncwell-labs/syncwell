// Syncwell browser client — zero dependencies, no build step.
// Usage:
//   import { Syncwell } from "./syncwell.js";
//   const sw = new Syncwell("ws://localhost:8080/ws", "my-room");
//   sw.on("snapshot", state => { ... });
//   sw.on("op", ({ key, register }) => { ... });
//   sw.connect();
//   sw.set("title", "Hello");
//
// Events: open, close, snapshot, op, presence, leave.
export class Syncwell {
  constructor(baseURL, room = "default") {
    this.url = `${baseURL}?room=${encodeURIComponent(room)}`;
    this.handlers = {};
    this.ws = null;
    this._reconnectDelay = 500;
  }

  on(event, cb) {
    (this.handlers[event] ||= []).push(cb);
    return this;
  }

  _emit(event, payload) {
    (this.handlers[event] || []).forEach((cb) => cb(payload));
  }

  connect() {
    this.ws = new WebSocket(this.url);
    this.ws.onopen = () => {
      this._reconnectDelay = 500;
      this._emit("open");
    };
    this.ws.onclose = () => {
      this._emit("close");
      // Exponential backoff reconnect — the client re-fetches a fresh
      // snapshot on reconnect, so no state is lost (see DESIGN.md).
      setTimeout(() => this.connect(), this._reconnectDelay);
      this._reconnectDelay = Math.min(this._reconnectDelay * 2, 8000);
    };
    this.ws.onmessage = (e) => {
      const msg = JSON.parse(e.data);
      this._emit(msg.type, msg);
    };
    return this;
  }

  _send(obj) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(obj));
    }
  }

  set(key, value) {
    this._send({ type: "set", key, value });
  }

  delete(key) {
    this._send({ type: "delete", key });
  }

  // Ephemeral, non-persisted broadcast (cursors, selections, who's-online).
  presence(data) {
    this._send({ type: "presence", data });
  }
}
