import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { LWWMap, stableJSON } from "./crdt.js";

// Shared fixtures with the Go implementation (internal/crdt/conformance_test.go).
// Canonical encodings must be byte-identical, and a tie must resolve to the same
// value in both languages, or replicas that see the same two writes in
// different orders would diverge.
const fixtureURL = new URL("../../tests/conformance/tiebreak_cases.json", import.meta.url);
const cases = JSON.parse(readFileSync(fixtureURL, "utf8"));

function canon(value) {
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canon).join(",")}]`;
  return `{${Object.keys(value)
    .sort()
    .map((k) => `${JSON.stringify(k)}:${canon(value[k])}`)
    .join(",")}}`;
}

for (const tc of cases) {
  test(`conformance: ${tc.name}`, () => {
    const a = { v: JSON.parse(tc.a.v), t: tc.a.t };

    if (tc.canonA !== undefined) {
      assert.equal(stableJSON(a.v), tc.canonA, "canonical A mismatch");
    }

    if (!tc.b) return;
    const b = { v: JSON.parse(tc.b.v), t: tc.b.t };
    if (tc.canonB !== undefined) {
      assert.equal(stableJSON(b.v), tc.canonB, "canonical B mismatch");
    }

    const ab = new LWWMap();
    ab.mergeRegister("k", a);
    ab.mergeRegister("k", b);

    const ba = new LWWMap();
    ba.mergeRegister("k", b);
    ba.mergeRegister("k", a);

    const gotAB = ab.get("k");
    const gotBA = ba.get("k");
    assert.equal(canon(gotAB), canon(gotBA), "result depends on merge order");
    assert.equal(canon(gotAB), canon(tc.want), "winner does not match fixture");
  });
}
