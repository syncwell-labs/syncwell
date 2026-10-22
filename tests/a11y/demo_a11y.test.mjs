import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";

const read = (path) => readFileSync(new URL(`../../${path}`, import.meta.url), "utf8");

test("demo index exposes accessible live-status and labelled inputs", () => {
  const html = read("examples/demos/index.html");
  assert.match(html, /role="status"/);
  assert.match(html, /aria-live="polite"/);
  assert.match(html, /label for="title"/);
  assert.match(html, /label for="doc"/);
});

test("collaborative text demo exposes a labelled editor and live status", () => {
  const html = read("examples/demos/sequence.html");
  assert.match(html, /role="status"/);
  assert.match(html, /aria-live="polite"/);
  assert.match(html, /label for="doc"/);
  assert.match(html, /collaborative text/i);
  assert.match(html, /id="undo"/);
  assert.match(html, /id="redo"/);
});

test("kanban demo exposes keyboard-friendly controls and labels", () => {
  const html = read("examples/demos/kanban.html");
  assert.match(html, /aria-live="polite"/);
  assert.match(html, /People here/);
  assert.match(html, /Delete card/);
  assert.match(html, /Add a card/);
});
