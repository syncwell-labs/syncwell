// Collaborative List/Array CRDT for Syncwell.
//
// Represents an ordered collection of arbitrary items (objects, strings, numbers)
// backed by fractional indexing and Syncwell's conflict-free LWW map.
//
// Supports:
//   - insert(index, item), push(...items), unshift(...items)
//   - delete(index), deleteById(id)
//   - move(fromIndex, toIndex) (conflict-free re-ordering without duplication)
//   - toArray(), toEntries(), get(index), length
import { rankBetween } from "./text.js";

export class CRDTArray {
  constructor(syncwell, opts = {}) {
    this.sw = syncwell;
    this.prefix = opts.prefix || "arr:";
    this._seq = 0;
    this._listeners = new Map();

    this._onChange = () => {
      const items = this.toArray();
      this.emit("change", items);
    };

    this.sw.on("change", this._onChange);
  }

  _nextId() {
    this._seq++;
    const rnd = Math.random().toString(36).slice(2, 7);
    return `${this.sw.clientId || "client"}-${Date.now().toString(36)}-${this._seq}-${rnd}`;
  }

  _nodes() {
    const live = this.sw.live();
    const nodes = [];
    for (const [k, v] of Object.entries(live)) {
      if (!k.startsWith(this.prefix) || !v || typeof v !== "object") continue;
      if (v.deleted || v.d) continue;
      nodes.push({
        id: v.id || k.slice(this.prefix.length),
        rank: String(v.rank ?? v.id ?? ""),
        value: v.value,
      });
    }
    nodes.sort((a, b) => {
      if (a.rank !== b.rank) return a.rank < b.rank ? -1 : 1;
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
    });
    return nodes;
  }

  get length() {
    return this._nodes().length;
  }

  toArray() {
    return this._nodes().map((n) => n.value);
  }

  toEntries() {
    return this._nodes().map((n) => ({ id: n.id, value: n.value }));
  }

  get(index) {
    const nodes = this._nodes();
    return nodes[index]?.value;
  }

  getId(index) {
    const nodes = this._nodes();
    return nodes[index]?.id;
  }

  insert(index, value) {
    const nodes = this._nodes();
    const idx = Math.max(0, Math.min(index, nodes.length));
    const leftRank = idx > 0 ? nodes[idx - 1].rank : "";
    const rightRank = idx < nodes.length ? nodes[idx].rank : null;
    const rank = rankBetween(leftRank, rightRank);
    const id = this._nextId();
    this.sw.set(this.prefix + id, { id, rank, value });
    return id;
  }

  push(...values) {
    let lastId;
    this.sw.batch(() => {
      for (const val of values) {
        lastId = this.insert(this.length, val);
      }
    });
    return lastId;
  }

  unshift(...values) {
    let firstId;
    this.sw.batch(() => {
      for (let i = values.length - 1; i >= 0; i--) {
        firstId = this.insert(0, values[i]);
      }
    });
    return firstId;
  }

  delete(index) {
    const nodes = this._nodes();
    const target = nodes[index];
    if (!target) return false;
    this.sw.set(this.prefix + target.id, { id: target.id, rank: target.rank, deleted: true });
    return true;
  }

  deleteById(id) {
    const live = this.sw.get(this.prefix + id);
    if (!live || live.deleted || live.d) return false;
    this.sw.set(this.prefix + id, { ...live, deleted: true });
    return true;
  }

  move(fromIndex, toIndex) {
    const nodes = this._nodes();
    if (fromIndex < 0 || fromIndex >= nodes.length) return false;
    if (toIndex < 0 || toIndex >= nodes.length) return false;
    if (fromIndex === toIndex) return true;

    const item = nodes[fromIndex];
    const remaining = nodes.filter((_, i) => i !== fromIndex);
    const targetIdx = Math.max(0, Math.min(toIndex, remaining.length));

    const leftRank = targetIdx > 0 ? remaining[targetIdx - 1].rank : "";
    const rightRank = targetIdx < remaining.length ? remaining[targetIdx].rank : null;
    const newRank = rankBetween(leftRank, rightRank);

    this.sw.set(this.prefix + item.id, { id: item.id, rank: newRank, value: item.value });
    return true;
  }

  clear() {
    const nodes = this._nodes();
    this.sw.batch(() => {
      for (const node of nodes) {
        this.sw.set(this.prefix + node.id, { id: node.id, rank: node.rank, deleted: true });
      }
    });
  }

  on(event, cb) {
    if (!this._listeners.has(event)) {
      this._listeners.set(event, []);
    }
    this._listeners.get(event).push(cb);
    return this;
  }

  off(event, cb) {
    const list = this._listeners.get(event);
    if (list) {
      const idx = list.indexOf(cb);
      if (idx !== -1) list.splice(idx, 1);
    }
    return this;
  }

  emit(event, ...args) {
    const list = this._listeners.get(event);
    if (list) {
      for (const cb of [...list]) cb(...args);
    }
  }

  destroy() {
    this.sw.off("change", this._onChange);
    this._listeners.clear();
  }
}
