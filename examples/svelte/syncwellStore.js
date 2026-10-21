// Svelte store adapter.
//
//   const room = createSyncwellStore({ url, room, clientId });
//   $room.state, $room.status, $room.peers
//   room.set("title", "hello"); room.undo();
//
// Install: npm i svelte syncwell-client
import { writable } from "svelte/store";
import { Syncwell } from "syncwell-client";
import { Awareness } from "syncwell-client/awareness";

export function createSyncwellStore({ url, room, token, clientId, user, persist = true }) {
  const store = writable({ state: {}, status: "connecting", peers: [], canUndo: false, canRedo: false });
  const sw = new Syncwell(url, room, { token, clientId, persist });
  const awareness = new Awareness(sw, { user: user || { id: clientId || sw.clientId }, start: false });

  const update = (patch) => store.update((current) => ({ ...current, ...patch }));
  const sync = () => update({ state: sw.live(), canUndo: sw.canUndo(), canRedo: sw.canRedo() });

  sw.on("change", sync);
  sw.on("history", sync);
  sw.on("open", () => {
    update({ status: "live" });
    awareness.start();
  });
  sw.on("close", () => update({ status: "offline" }));
  awareness.on("update", (peers) => update({ peers }));
  sw.ready.then(() => {
    sync();
    sw.connect();
  });

  return {
    subscribe: store.subscribe,
    set: (key, value) => sw.set(key, value),
    remove: (key) => sw.delete(key),
    undo: () => sw.undo(),
    redo: () => sw.redo(),
    cursor: (index, end = index) => awareness.setSelection(index, end),
    destroy() {
      awareness.close();
      sw.close();
    },
  };
}
