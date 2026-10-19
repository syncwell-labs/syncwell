import { readFileSync } from "node:fs";
import { test } from "node:test";
import assert from "node:assert/strict";

const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");

test("demo index exposes accessible live-status and labelled inputs", () => {
  const html = read("demo/index.html");
  assert.match(html, /role="status"/);
  assert.match(html, /aria-live="polite"/);
  assert.match(html, /label for="title"/);
  assert.match(html, /label for="doc"/);
});

test("sequence scratchpad exposes a labelled textarea and live status", () => {
  const html = read("demo/sequence.html");
  assert.match(html, /role="status"/);
  assert.match(html, /aria-live="polite"/);
  assert.match(html, /label for="doc"/);
  assert.match(html, /sequence scratchpad/i);
});

test("kanban demo exposes keyboard-friendly controls and labels", () => {
  const html = read("demo/kanban.html");
  assert.match(html, /aria-live="polite"/);
  assert.match(html, /connected peers/);
  assert.match(html, /Delete card/);
  assert.match(html, /Add a card/);
});
