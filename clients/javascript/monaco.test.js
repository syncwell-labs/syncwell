import { test } from "node:test";
import assert from "node:assert/strict";
import { bindMonaco } from "./monaco.js";

function fakeModel(initial = "") {
  let text = initial;
  const contentListeners = [];
  return {
    getValue() {
      return text;
    },
    setValue(val) {
      text = val;
    },
    getFullModelRange() {
      return { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: text.length + 1 };
    },
    pushEditOperations(_before, edits, _cursor) {
      for (const edit of edits) {
        text = edit.text;
      }
    },
    onDidChangeContent(cb) {
      contentListeners.push(cb);
      return {
        dispose: () => {
          const idx = contentListeners.indexOf(cb);
          if (idx !== -1) contentListeners.splice(idx, 1);
        },
      };
    },
    triggerContentChange(changes) {
      for (const cb of contentListeners) cb({ changes });
    },
    getPositionAt(offset) {
      return { lineNumber: 1, column: offset + 1 };
    },
    getOffsetAt(pos) {
      return pos.column - 1;
    },
  };
}

function fakeSequenceText(initial = "") {
  let text = initial;
  const listeners = {};
  return {
    text() {
      return text;
    },
    insertAt(offset, str) {
      text = text.slice(0, offset) + str + text.slice(offset);
      this.emitChange();
    },
    deleteAt(offset, count = 1) {
      text = text.slice(0, offset) + text.slice(offset + count);
      this.emitChange();
    },
    replaceRange(offset, count, str) {
      text = text.slice(0, offset) + str + text.slice(offset + count);
      this.emitChange();
    },
    on(event, cb) {
      (listeners[event] ||= []).push(cb);
    },
    off(event, cb) {
      listeners[event] = (listeners[event] || []).filter((fn) => fn !== cb);
    },
    emitChange() {
      for (const cb of listeners["change"] || []) cb(text);
    },
  };
}

test("bindMonaco synchronizes editor and sequenceText bidirectionally", () => {
  const model = fakeModel();
  const seqText = fakeSequenceText("initial text");

  const binding = bindMonaco(model, seqText);
  assert.equal(model.getValue(), "initial text");

  // 1. Local editor change -> SequenceText
  model.triggerContentChange([
    {
      rangeOffset: 8,
      rangeLength: 4, // replace "text"
      text: "code",
    },
  ]);
  assert.equal(seqText.text(), "initial code");

  // 2. Remote SequenceText change -> Editor
  seqText.insertAt(0, "prefix ");
  assert.equal(model.getValue(), "prefix initial code");

  binding.destroy();
});
