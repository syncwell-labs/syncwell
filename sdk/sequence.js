// Tiny state-based sequence CRDT for collaborative text foundations.
// Elements are inserted after a parent id (or the empty root) and render in a
// deterministic order even when concurrent replicas merge in different orders.

function stableNodeKey(node) {
  return JSON.stringify([
    node.id || "",
    node.after || "",
    node.text || "",
    Boolean(node.deleted),
  ]);
}

function betterNode(a, b) {
  if ((a.after || "") !== (b.after || "")) return (a.after || "") > (b.after || "");
  if (Boolean(a.deleted) !== Boolean(b.deleted)) return Boolean(a.deleted);
  if ((a.text || "") !== (b.text || "")) return (a.text || "") > (b.text || "");
  return stableNodeKey(a) > stableNodeKey(b);
}

export class SequenceDoc {
  constructor() {
    this.nodes = new Map();
  }

  apply(node) {
    if (!node?.id) return;
    const cur = this.nodes.get(node.id);
    if (!cur || betterNode(node, cur)) this.nodes.set(node.id, {
      id: node.id,
      after: node.after || "",
      text: node.text || "",
      deleted: Boolean(node.deleted),
    });
  }

  insert(id, after, text) {
    this.apply({ id, after, text });
  }

  delete(id) {
    const cur = this.nodes.get(id);
    this.apply({ id, after: cur?.after || "", text: cur?.text || "", deleted: true });
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

  _children(parent) {
    const ids = [];
    for (const [id, node] of this.nodes) {
      if ((node.after || "") === parent) ids.push(id);
    }
    ids.sort();
    return ids;
  }

  text() {
    const out = [];
    const visited = new Set();
    const walk = (parent) => {
      for (const id of this._children(parent)) {
        if (visited.has(id)) continue;
        visited.add(id);
        const node = this.nodes.get(id);
        if (node && !node.deleted) out.push(node.text);
        walk(id);
      }
    };
    walk("");
    return out.join("");
  }

  visibleIds() {
    const out = [];
    const visited = new Set();
    const walk = (parent) => {
      for (const id of this._children(parent)) {
        if (visited.has(id)) continue;
        visited.add(id);
        const node = this.nodes.get(id);
        if (node && !node.deleted) out.push(id);
        walk(id);
      }
    };
    walk("");
    return out;
  }
}
