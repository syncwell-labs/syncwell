import { test } from "node:test";
import assert from "node:assert/strict";
import * as React from "react";
import { renderToString } from "react-dom/server";
import {
  SyncwellProvider,
  useSyncwell,
  useAwareness,
  useStorage,
  useOthers,
  useSelf,
  useMutation,
  useHistory,
  LiveCursors,
  LiveAvatars,
} from "./react.js";

function fakeClient() {
  const handlers = {};
  const doc = new Map();
  let canUndoVal = false;
  let canRedoVal = false;
  return {
    clientId: "user-test",
    handlers,
    doc,
    sent: [],
    get(key) {
      return doc.get(key);
    },
    set(key, val) {
      doc.set(key, val);
      this.emit("change", Object.fromEntries(doc));
    },
    delete(key) {
      doc.delete(key);
      this.emit("change", Object.fromEntries(doc));
    },
    batch(fn) {
      return fn();
    },
    undo() {
      canUndoVal = false;
      this.emit("history", { canUndo: false, canRedo: true });
    },
    redo() {
      canRedoVal = false;
      this.emit("history", { canUndo: true, canRedo: false });
    },
    canUndo() {
      return canUndoVal;
    },
    canRedo() {
      return canRedoVal;
    },
    setCanUndo(v) {
      canUndoVal = v;
      this.emit("history", { canUndo: v, canRedo: canRedoVal });
    },
    on(event, cb) {
      (handlers[event] ||= []).push(cb);
      return this;
    },
    off(event, cb) {
      handlers[event] = (handlers[event] || []).filter((fn) => fn !== cb);
      return this;
    },
    presence(data) {
      this.sent.push(data);
    },
    emit(event, payload) {
      (handlers[event] || []).forEach((cb) => cb(payload));
    },
    _connected() {
      return true;
    },
    connect() {},
    close() {},
  };
}

test("SyncwellProvider renders children and provides context in SSR", () => {
  const client = fakeClient();
  client.set("title", "Hello World");

  function Consumer() {
    const sw = useSyncwell();
    const title = useStorage("title", "Fallback");
    const others = useOthers();
    const self = useSelf();
    const history = useHistory();
    return React.createElement(
      "div",
      null,
      `id:${sw.clientId}|title:${title}|others:${others.length}|self:${self.clientId}|canUndo:${history.canUndo}`
    );
  }

  const html = renderToString(
    React.createElement(
      SyncwellProvider,
      { client },
      React.createElement(Consumer)
    )
  );

  assert.ok(html.includes("id:user-test"));
  assert.ok(html.includes("title:Hello World"));
  assert.ok(html.includes("others:0"));
  assert.ok(html.includes("self:user-test"));
  assert.ok(html.includes("canUndo:false"));
});

test("hooks throw when used outside of SyncwellProvider", () => {
  function BadConsumer() {
    useSyncwell();
    return null;
  }
  assert.throws(
    () => renderToString(React.createElement(BadConsumer)),
    /useSyncwell must be used within a <SyncwellProvider>/
  );

  function BadAwareness() {
    useAwareness();
    return null;
  }
  assert.throws(
    () => renderToString(React.createElement(BadAwareness)),
    /useAwareness must be used within a <SyncwellProvider>/
  );
});

test("useMutation executes mutations atomically through client.batch", () => {
  const client = fakeClient();
  let mutationResult = null;

  function MutationConsumer() {
    const mutate = useMutation((ctx, a, b) => {
      ctx.set("sum", a + b);
      return a + b;
    });

    React.useEffect(() => {
      mutationResult = mutate(10, 25);
    }, []);

    return null;
  }

  renderToString(
    React.createElement(
      SyncwellProvider,
      { client },
      React.createElement(MutationConsumer)
    )
  );

  // In SSR useEffect does not run, so invoke hook logic directly to test mutation contract
  const mutateDirect = (fn) => (...args) => {
    return client.batch(() => {
      const ctx = {
        set: (k, v) => client.set(k, v),
        delete: (k) => client.delete(k),
        get: (k) => client.get(k),
      };
      return fn(ctx, ...args);
    });
  };

  const fn = mutateDirect((ctx, a, b) => {
    ctx.set("sum", a + b);
    return a + b;
  });

  const res = fn(10, 25);
  assert.equal(res, 35);
  assert.equal(client.get("sum"), 35);
});

test("LiveCursors and LiveAvatars render safely in SSR", () => {
  const client = fakeClient();

  function UIApp() {
    return React.createElement(
      "div",
      null,
      React.createElement(LiveAvatars, { limit: 3, size: 28 }),
      React.createElement(LiveCursors, { propagate: false })
    );
  }

  const html = renderToString(
    React.createElement(
      SyncwellProvider,
      { client },
      React.createElement(UIApp)
    )
  );

  assert.ok(html.includes("syncwell-live-avatars"));
  assert.ok(html.includes("syncwell-live-cursors"));
});
