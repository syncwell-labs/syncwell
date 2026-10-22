// Pure unit tests for the JS CRDT — the same laws proven on the Go side, so the
// two implementations are guaranteed to agree. Run: node --test (from clients/javascript/).
import { test } from "node:test";
import assert from "node:assert/strict";
import { LWWMap, Clock, tsAfter } from "./crdt.js";
import { SequenceDoc } from "./sequence.js";

const ts = (c, n) => ({ c, n });

function snapEqual(a, b) {
  assert.deepEqual(a.snapshot(), b.snapshot());
}
function clone(m) {
  const c = new LWWMap();
  c.mergeState(m.snapshot());
  return c;
}

test("last writer wins; stale write ignored", () => {
  const m = new LWWMap();
  m.set("title", "old", ts(1, "A"));
  m.set("title", "new", ts(2, "A"));
  assert.equal(m.get("title"), "new");
  m.set("title", "stale", ts(1, "Z"));
  assert.equal(m.get("title"), "new");
});

test("tombstone ordering: delete and write compete by timestamp", () => {
  const m = new LWWMap();
  m.set("k", "v", ts(1, "A"));
  m.delete("k", ts(2, "A"));
  assert.equal(m.get("k"), undefined);
  m.set("k", "back", ts(3, "A"));
  assert.equal(m.get("k"), "back");
  m.delete("k", ts(1, "A")); // stale delete
  assert.equal(m.get("k"), "back");
});

test("merge is idempotent", () => {
  const a = new LWWMap();
  a.set("x", "1", ts(1, "A"));
  a.set("y", "2", ts(2, "A"));
  const before = JSON.stringify(a.snapshot());
  a.mergeState(clone(a).snapshot());
  assert.equal(JSON.stringify(a.snapshot()), before);
});

test("merge is commutative", () => {
  const a = new LWWMap();
  a.set("k", "a-wins", ts(5, "A"));
  a.set("only-a", "1", ts(1, "A"));
  const b = new LWWMap();
  b.set("k", "b-loses", ts(3, "B"));
  b.set("only-b", "2", ts(1, "B"));

  const ab = clone(a);
  ab.mergeState(b.snapshot());
  const ba = clone(b);
  ba.mergeState(a.snapshot());

  snapEqual(ab, ba);
  assert.equal(ab.get("k"), "a-wins");
});

test("equal timestamp conflicts use a deterministic content tie-break", () => {
  const a = new LWWMap(); a.set("k", "alpha", ts(9, "same-client"));
  const b = new LWWMap(); b.set("k", "bravo", ts(9, "same-client"));

  const ab = clone(a); ab.mergeState(b.snapshot());
  const ba = clone(b); ba.mergeState(a.snapshot());

  snapEqual(ab, ba);
  assert.equal(ab.get("k"), "bravo");
});

test("equal timestamp delete deterministically wins over a value", () => {
  const a = new LWWMap(); a.set("k", "value", ts(9, "same-client"));
  const b = new LWWMap(); b.delete("k", ts(9, "same-client"));
  a.mergeState(b.snapshot());
  assert.equal(a.get("k"), undefined);
});

test("merge is associative", () => {
  const a = new LWWMap(); a.set("k", "a", ts(1, "A"));
  const b = new LWWMap(); b.set("k", "b", ts(2, "B"));
  const c = new LWWMap(); c.set("k", "c", ts(3, "C"));

  const left = clone(a); left.mergeState(b.snapshot()); left.mergeState(c.snapshot());
  const bc = clone(b); bc.mergeState(c.snapshot());
  const right = clone(a); right.mergeState(bc.snapshot());

  snapEqual(left, right);
});

test("convergence under concurrent edits (with offline-style local clocks)", () => {
  const ca = new Clock("A");
  const cb = new Clock("B");
  const a = new LWWMap();
  const b = new LWWMap();

  a.set("title", "A's title", ca.tick());
  a.set("a-note", "hi from A", ca.tick());
  a.delete("shared", ca.tick());

  b.set("title", "B's title", cb.tick());
  b.set("b-note", "hi from B", cb.tick());
  b.set("shared", "B keeps shared", cb.tick());

  // Exchange full state (merge twice to prove duplicate delivery is harmless).
  a.mergeState(b.snapshot());
  b.mergeState(a.snapshot());
  a.mergeState(b.snapshot());

  snapEqual(a, b);
  assert.equal(a.get("a-note"), "hi from A");
  assert.equal(a.get("b-note"), "hi from B");
});

test("tsAfter ordering: counter then node", () => {
  assert.equal(tsAfter(ts(2, "A"), ts(1, "Z")), true);
  assert.equal(tsAfter(ts(1, "B"), ts(1, "A")), true);
  assert.equal(tsAfter(ts(1, "A"), ts(1, "B")), false);
});

test("sequence CRDT merges deterministically across replicas", () => {
  const a = new SequenceDoc();
  a.insert("a1", "", "H");
  a.insert("a2", "a1", "i");

  const b = new SequenceDoc();
  b.insert("b1", "", "!");
  b.insert("b2", "b1", " ");

  const ab = new SequenceDoc();
  ab.merge(a.snapshot());
  ab.merge(b.snapshot());

  const ba = new SequenceDoc();
  ba.merge(b.snapshot());
  ba.merge(a.snapshot());

  assert.equal(ab.text(), ba.text());
  assert.equal(ab.text(), "Hi! ");
  assert.deepEqual(ab.visibleIds(), ["a1", "a2", "b1", "b2"]);
});

test("sequence CRDT tombstones hide deleted nodes", () => {
  const doc = new SequenceDoc();
  doc.insert("root", "", "x");
  doc.delete("root");
  assert.equal(doc.text(), "");
  assert.deepEqual(doc.visibleIds(), []);
});
