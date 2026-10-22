// Tiny state-based sequence CRDT for collaborative text foundations.
// Elements are inserted after a parent id (or the empty root) and render in a
// deterministic order even when concurrent replicas merge in different orders.
//
// Newer elements may also carry a `rank` (a lexicographic fractional index).
// When any node has a rank, the document renders as a flat ordered sequence by
// (rank, id), which is what makes arbitrary middle insertions position-stable
// under concurrency. Legacy tree-only documents keep rendering by walking the
// parent chain, so older snapshots remain readable.

function stableNodeKey(node) {
  return JSON.stringify([
    node.id || "",
    node.after || "",
    node.rank || "",
    node.text || "",
    Boolean(node.deleted),
    node.marks || null,
  ]);
}

function betterNode(a, b) {
  if ((a.rank || "") !== (b.rank || "")) return (a.rank || "") > (b.rank || "");
  if ((a.after || "") !== (b.after || "")) return (a.after || "") > (b.after || "");
  if (Boolean(a.deleted) !== Boolean(b.deleted)) return Boolean(a.deleted);
  if ((a.text || "") !== (b.text || "")) return (a.text || "") > (b.text || "");
  return stableNodeKey(a) > stableNodeKey(b);
}

function normalise(node) {
  const out = {
    id: node.id,
    after: node.after || "",
    text: node.text || "",
    deleted: Boolean(node.deleted),
  };
  if (node.rank !== undefined) out.rank = String(node.rank);
  if (node.marks && typeof node.marks === "object") out.marks = node.marks;
  return out;
}

export class SequenceDoc {
  constructor() {
    this.nodes = new Map();
  }

  apply(node) {
    if (!node?.id) return;
    const next = normalise(node);
    const cur = this.nodes.get(next.id);
    if (!cur || betterNode(next, cur)) this.nodes.set(next.id, next);
  }

  insert(id, after, text) {
    this.apply({ id, after, text });
  }

  /** Insert an element with an explicit fractional rank. */
  insertRanked(id, rank, text, after = "") {
    this.apply({ id, after, rank, text });
  }

  delete(id) {
    const cur = this.nodes.get(id);
    if (!cur) return;
    this.apply({ ...cur, deleted: true });
  }

  /** Set or clear rich-text marks on a visible element. */
  mark(id, marks) {
    const cur = this.nodes.get(id);
    if (!cur) return;
    this.apply({ ...cur, marks: marks || undefined });
  }

  merge(other) {
    const snap = typeof other?.snapshot === "function" ? other.snapshot() : other || {};
    for (const node of Object.values(snap)) {
      this.apply(node);
    }
  }

  snapshot() {
    const out = {};
    for (const [id, node] of this.nodes) out[id] = node;
    return out;
  }

  _ranked() {
    for (const node of this.nodes.values()) {
      if (node.rank !== undefined) return true;
    }
    return false;
  }

  _children(parent) {
    const ids = [];
    for (const [id, node] of this.nodes) {
      if ((node.after || "") === parent) ids.push(id);
    }
    ids.sort();
    return ids;
  }

  _ordered() {
    const all = [...this.nodes.values()];
    if (!this._ranked()) {
      const out = [];
      const visited = new Set();
      const walk = (parent) => {
        for (const id of this._children(parent)) {
          if (visited.has(id)) continue;
          visited.add(id);
          out.push(this.nodes.get(id));
          walk(id);
        }
      };
      walk("");
      return out;
    }
    // Flat rank order; ties on rank fall back to the unique element id so every
    // replica computes the same layout.
    all.sort((a, b) => {
      const ra = a.rank ?? a.id;
      const rb = b.rank ?? b.id;
      if (ra !== rb) return ra < rb ? -1 : 1;
      return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
    });
    return all;
  }

  text() {
    return this._ordered()
      .filter((node) => node && !node.deleted)
      .map((node) => node.text)
      .join("");
  }

  visibleIds() {
    return this._ordered()
      .filter((node) => node && !node.deleted)
      .map((node) => node.id);
  }
}
