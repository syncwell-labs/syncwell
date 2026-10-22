// Collaborative text built on the Syncwell LWW map and the sequence CRDT.
//
// Every character is one register in the room's LWW map. A register value is a
// sequence node: `{ id, rank, text, deleted, marks? }`. `rank` is a fractional
// index so an insert in the middle gets a position that stays between its two
// neighbours even when another client inserts concurrently at the same spot.
//
// Usage:
//   import { Syncwell } from "syncwell-client";
//   import { SequenceText } from "syncwell-client/text";
//   const sw = new Syncwell(url, "notes", { persist: true });
//   const text = new SequenceText(sw, { prefix: "body:" });
//   text.insertAt(0, "Hello");
//   text.on("change", () => render(text.text()));
//
// The wrapper writes through Syncwell, so offline edits, reconnect merging,
// undo/redo, and persistence all work exactly as they do for map documents.
import { SequenceDoc } from "./sequence.js";

const DIGITS = "0123456789abcdefghijklmnopqrstuvwxyz";

/** Return a rank strictly between `left` and `right` (right may be null). */
export function rankBetween(left, right) {
  left = left || "";
  if (right == null) return left === "" ? "m" : left + "m";
  if (left === "") {
    const i = DIGITS.indexOf(right[0]);
    if (i > 0) return DIGITS[Math.floor(i / 2)];
    // right starts with "0": "0" is still below a longer "0…" key.
    return right.length > 1 ? "0" : "";
  }
  let i = 0;
  while (i < left.length && i < right.length && left[i] === right[i]) i++;
  if (i === left.length) {
    const tail = right.slice(i);
    return left + (tail === "" ? "m" : rankBetween("", tail));
  }
  const li = DIGITS.indexOf(left[i]);
  const ri = DIGITS.indexOf(right[i]);
  if (ri - li > 1) return left.slice(0, i) + DIGITS[(li + ri) >> 1];
  // Adjacent digits: extending the left key keeps it below the right key.
  return left + "m";
}

function clamp(n, min, max) {
  return Math.max(min, Math.min(max, n));
}

function nodeValue(node) {
  const out = { id: node.id, rank: node.rank, text: node.text };
  if (node.after) out.after = node.after;
  if (node.deleted) out.d = true;
  if (node.marks) out.marks = node.marks;
  return out;
}

export class SequenceText {
  constructor(syncwell, opts = {}) {
    this.sw = syncwell;
    this.prefix = opts.prefix || "text:";
    this.clientId = opts.clientId || syncwell.clientId;
    this.doc = new SequenceDoc();
    this.handlers = {};
    this._seq = 0;
    this._hydrate();
    this._onChange = () => this._pull();
    syncwell.on("change", this._onChange);
  }

  on(event, cb) {
    (this.handlers[event] ||= []).push(cb);
    return this;
  }
  _emit(event, payload) {
    (this.handlers[event] || []).forEach((cb) => cb(payload));
  }

  _isOurs(key) {
    return key.startsWith(this.prefix);
  }

  _hydrate() {
    const snap = this.sw.doc.snapshot();
    const next = new SequenceDoc();
    for (const [key, reg] of Object.entries(snap)) {
      if (!this._isOurs(key) || reg.deleted || !reg.v || typeof reg.v !== "object") continue;
      const v = reg.v;
      next.apply({
        id: v.id || key.slice(this.prefix.length),
        after: v.after || "",
        rank: v.rank,
        text: v.text || "",
        deleted: Boolean(v.d ?? v.deleted),
        marks: v.marks,
      });
    }
    this.doc = next;
  }

  _pull() {
    this._hydrate();
    this._emit("change", this.text());
  }

  _key(id) {
    return this.prefix + id;
  }

  _newId() {
    this._seq += 1;
    return `${this.clientId}:${this._seq}`;
  }

  _rankOf(id) {
    return this.doc.nodes.get(id)?.rank ?? (id || "");
  }

  _writeNode(node) {
    this.sw.set(this._key(node.id), nodeValue(node));
    this.doc.apply(node);
  }

  /** Insert text at a character offset. Returns the inserted element ids. */
  insertAt(index, value) {
    const ids = this.doc.visibleIds();
    index = clamp(index, 0, ids.length);
    const leftId = index > 0 ? ids[index - 1] : "";
    const rightId = index < ids.length ? ids[index] : null;
    let leftRank = this._rankOf(leftId);
    const rightRank = rightId ? this._rankOf(rightId) : null;

    const inserted = [];
    this.sw.batch(() => {
      let after = leftId;
      for (const ch of Array.from(value)) {
        const rank = rankBetween(leftRank, rightRank);
        const id = this._newId();
        const node = { id, after, rank, text: ch };
        this._writeNode(node);
        inserted.push(id);
        leftRank = rank;
        after = id;
      }
    });
    return inserted;
  }

  /** Delete `count` code points starting at offset `index`. */
  deleteAt(index, count = 1) {
    const ids = this.doc.visibleIds();
    index = clamp(index, 0, ids.length);
    count = clamp(count, 0, ids.length - index);
    const removed = ids.slice(index, index + count);
    this.sw.batch(() => {
      for (const id of removed) {
        const node = this.doc.nodes.get(id);
        if (node) this._writeNode({ ...node, deleted: true });
      }
    });
    return removed.length;
  }

  /** Replace a range with new text (one undoable batch). */
  replaceRange(index, count, value) {
    this.sw.batch(() => {
      this.deleteAt(index, count);
      this.insertAt(index, value);
    });
  }

  /** Apply a rich-text mark to a visible range; null clears the mark. */
  markRange(index, count, marks) {
    const ids = this.doc.visibleIds();
    index = clamp(index, 0, ids.length);
    count = clamp(count, 0, ids.length - index);
    this.sw.batch(() => {
      for (const id of ids.slice(index, index + count)) {
        const node = this.doc.nodes.get(id);
        if (node) this._writeNode({ ...node, marks });
      }
    });
  }

  text() {
    return this.doc.text();
  }

  visibleIds() {
    return this.doc.visibleIds();
  }

  marksAt(index) {
    return this.doc.nodes.get(this.doc.visibleIds()[index])?.marks;
  }

  get length() {
    return this.doc.visibleIds().length;
  }

  close() {
    if (this._onChange && typeof this.sw.off === "function") this.sw.off("change", this._onChange);
    this._onChange = null;
  }
}
