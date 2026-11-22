import { test } from "node:test";
import assert from "node:assert/strict";
import { CRDTArray } from "./array.js";
import { Clock, LWWMap } from "./crdt.js";

function fakeSyncwell(nodeId = "replica-a") {
  const map = new LWWMap();
  const clock = new Clock(nodeId);
  const listeners = new Map();

  return {
    clientId: nodeId,
    live() {
      return map.live();
    },
    get(key) {
      return map.get(key);
    },
    set(key, value) {
      map.set(key, value, clock.tick());
      this.emit("change", map.live());
    },
    batch(fn) {
      return fn();
    },
    on(ev, cb) {
      (listeners.get(ev) || listeners.set(ev, []).get(ev)).push(cb);
      return this;
    },
    off(ev, cb) {
      const list = listeners.get(ev);
      if (list) {
        const i = list.indexOf(cb);
        if (i !== -1) list.splice(i, 1);
      }
      return this;
    },
    emit(ev, ...args) {
      const list = listeners.get(ev);
      if (list) {
        for (const cb of [...list]) cb(...args);
      }
    },
    _map: map,
    _clock: clock,
  };
}

test("CRDTArray insert, push, unshift, and get", () => {
  const sw = fakeSyncwell();
  const arr = new CRDTArray(sw, { prefix: "tasks:" });

  assert.equal(arr.length, 0);
  arr.push("Task 1", "Task 2");
  assert.equal(arr.length, 2);
  assert.deepEqual(arr.toArray(), ["Task 1", "Task 2"]);

  arr.unshift("Task 0");
  assert.deepEqual(arr.toArray(), ["Task 0", "Task 1", "Task 2"]);

  // Insert in middle
  arr.insert(1, "Task 0.5");
  assert.deepEqual(arr.toArray(), ["Task 0", "Task 0.5", "Task 1", "Task 2"]);
  assert.equal(arr.get(1), "Task 0.5");
});

test("CRDTArray move reorders items without duplication", () => {
  const sw = fakeSyncwell();
  const arr = new CRDTArray(sw, { prefix: "kanban:" });

  arr.push("A", "B", "C", "D");
  assert.deepEqual(arr.toArray(), ["A", "B", "C", "D"]);

  // Move "D" to index 0: ["D", "A", "B", "C"]
  arr.move(3, 0);
  assert.deepEqual(arr.toArray(), ["D", "A", "B", "C"]);

  // Move "A" to index 2: ["D", "B", "A", "C"]
  arr.move(1, 2);
  assert.deepEqual(arr.toArray(), ["D", "B", "A", "C"]);
});

test("CRDTArray delete and deleteById", () => {
  const sw = fakeSyncwell();
  const arr = new CRDTArray(sw, { prefix: "items:" });

  arr.push("Apple", "Banana", "Cherry");
  const bananaId = arr.getId(1);
  assert.ok(bananaId);

  // Delete Cherry by index 2
  arr.delete(2);
  assert.deepEqual(arr.toArray(), ["Apple", "Banana"]);

  // Delete Banana by id
  arr.deleteById(bananaId);
  assert.deepEqual(arr.toArray(), ["Apple"]);
});

test("CRDTArray concurrent replicas merge deterministically", () => {
  const r1 = fakeSyncwell("node-1");
  const r2 = fakeSyncwell("node-2");

  const arr1 = new CRDTArray(r1, { prefix: "shared:" });
  const arr2 = new CRDTArray(r2, { prefix: "shared:" });

  arr1.push("Base Item");
  // Sync state from r1 to r2
  r2._map.mergeState(r1._map.snapshot());
  r2.emit("change", r2.live());

  assert.deepEqual(arr2.toArray(), ["Base Item"]);

  // Concurrent edits: r1 pushes "R1 Item", r2 pushes "R2 Item"
  arr1.push("R1 Item");
  arr2.push("R2 Item");

  // Merge both directions
  r1._map.mergeState(r2._map.snapshot());
  r2._map.mergeState(r1._map.snapshot());
  r1.emit("change", r1.live());
  r2.emit("change", r2.live());

  assert.equal(arr1.length, 3);
  assert.equal(arr2.length, 3);
  // Both replicas must converge to identical ordered list!
  assert.deepEqual(arr1.toArray(), arr2.toArray());
});
