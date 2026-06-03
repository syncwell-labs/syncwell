// Browser/Node port of the Syncwell CRDT. This is a faithful mirror of the Go
// implementation in internal/crdt — the *same* merge rule runs on client and
// server, which is what lets a client edit offline and converge on reconnect.
//
// A register is { v: <value>, t: { c: <counter>, n: <node> }, d?: true }.

// tsAfter reports whether timestamp a wins over b (higher counter; ties by node).
export function tsAfter(a, b) {
  if (a.c !== b.c) return a.c > b.c;
  return a.n > b.n;
}

// Clock is a per-replica Lamport clock.
export class Clock {
  constructor(node) {
    this.node = node;
    this.counter = 0;
  }
  tick() {
    this.counter += 1;
    return { c: this.counter, n: this.node };
  }
  // observe keeps this clock from lagging behind a causally-prior event, so a
  // fresh local edit always outranks state the client has already seen.
  observe(ts) {
    if (ts && ts.c > this.counter) this.counter = ts.c;
  }
}

// LWWMap is the state-based last-writer-wins map.
export class LWWMap {
  constructor() {
    this.entries = new Map(); // key -> register
  }

  _apply(key, reg) {
    const cur = this.entries.get(key);
    if (!cur || tsAfter(reg.t, cur.t)) this.entries.set(key, reg);
  }

  set(key, value, ts) {
    this._apply(key, { v: value, t: ts });
  }
  delete(key, ts) {
    this._apply(key, { t: ts, d: true });
  }

  // mergeRegister folds a single incoming register (per-key CRDT join).
  mergeRegister(key, reg) {
    this._apply(key, reg);
  }
  // mergeState folds a full state object { key: register, ... }.
  mergeState(state) {
    for (const k of Object.keys(state || {})) this._apply(k, state[k]);
  }

  get(key) {
    const r = this.entries.get(key);
    return !r || r.d ? undefined : r.v;
  }

  // snapshot returns a plain object of all registers (incl. tombstones).
  snapshot() {
    const out = {};
    for (const [k, r] of this.entries) out[k] = r;
    return out;
  }

  // live returns only the non-deleted key/value pairs (the visible document).
  live() {
    const out = {};
    for (const [k, r] of this.entries) if (!r.d) out[k] = r.v;
    return out;
  }
}
