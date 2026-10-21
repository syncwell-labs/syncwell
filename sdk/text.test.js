import { test } from "node:test";
import assert from "node:assert/strict";
import { Syncwell } from "./syncwell.js";
import { SequenceText, rankBetween } from "./text.js";

class FakeWebSocket {
  static OPEN = 1;
  constructor(url) {
    this.url = url;
    this.readyState = FakeWebSocket.OPEN;
    this.sent = [];
  }
  send(message) {
    this.sent.push(message);
  }
  close() {
    this.readyState = 3;
  }
}

function newText(id) {
  const sw = new Syncwell("ws://localhost/ws", "text-room", {
    clientId: id,
    WebSocket: FakeWebSocket,
  });
  return { sw, text: new SequenceText(sw, { prefix: "body:" }) };
}

test("rankBetween returns a key strictly between its neighbours", () => {
  const cases = [
    ["", "m"],
    ["m", null],
    ["a", "b"],
    ["a", "am"],
    ["az", "b"],
  ];
  for (const [left, right] of cases) {
    const got = rankBetween(left, right);
    if (right != null) assert.ok(got > left && got < right, `${left} < ${got} < ${right}`);
    else assert.ok(got > left, `${got} > ${left}`);
  }
});

test("insert, delete, replace and marks operate on character offsets", () => {
  const { text } = newText("A");
  text.insertAt(0, "Hello");
  assert.equal(text.text(), "Hello");
  text.insertAt(5, " world");
  assert.equal(text.text(), "Hello world");
  text.insertAt(5, ",");
  assert.equal(text.text(), "Hello, world");
  text.deleteAt(5, 1);
  assert.equal(text.text(), "Hello world");
  text.replaceRange(6, 5, "there");
  assert.equal(text.text(), "Hello there");
  text.markRange(0, 5, { bold: true });
  assert.deepEqual(text.marksAt(0), { bold: true });
  assert.equal(text.length, 11);
});

test("concurrent inserts at the same offset converge deterministically", () => {
  const a = newText("A");
  const b = newText("B");
  a.text.insertAt(0, "ac");
  // Both replicas now know "ac"; each inserts a different character between a and c.
  b.sw.doc.mergeState(a.sw.doc.snapshot());
  b.text._pull();
  a.text.insertAt(1, "X");
  b.text.insertAt(1, "Y");

  const ab = newText("AB");
  ab.sw.doc.mergeState(a.sw.doc.snapshot());
  ab.sw.doc.mergeState(b.sw.doc.snapshot());
  ab.text._pull();

  const ba = newText("BA");
  ba.sw.doc.mergeState(b.sw.doc.snapshot());
  ba.sw.doc.mergeState(a.sw.doc.snapshot());
  ba.text._pull();

  assert.equal(ab.text.text(), ba.text.text());
  assert.equal(ab.text.text().length, 4);
  assert.ok(["aXYc", "aYXc"].includes(ab.text.text()));
});

test("deleting one replica's character converges with divergent edits", () => {
  const a = newText("A");
  const b = newText("B");
  a.text.insertAt(0, "abcdef");
  b.sw.doc.mergeState(a.sw.doc.snapshot());
  b.text._pull();

  a.text.deleteAt(2, 2); // remove "cd"
  b.text.insertAt(6, "!");
  assert.equal(a.text.text(), "abef");
  assert.equal(b.text.text(), "abcdef!");

  a.sw.doc.mergeState(b.sw.doc.snapshot());
  a.text._pull();
  b.sw.doc.mergeState(a.sw.doc.snapshot());
  b.text._pull();
  assert.equal(a.text.text(), b.text.text());
  assert.equal(a.text.text(), "abef!");
});

test("text edits are undoable and redoable as one batch", () => {
  const { text, sw } = newText("A");
  text.insertAt(0, "hello");
  assert.equal(text.text(), "hello");
  assert.equal(sw.undo(), true);
  assert.equal(text.text(), "");
  assert.equal(sw.redo(), true);
  assert.equal(text.text(), "hello");
});
