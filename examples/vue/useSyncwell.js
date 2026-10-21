// Vue 3 composable.
//
//   const { state, status, peers, set, undo, redo } = useSyncwell({...});
//
// Install: npm i vue syncwell-client
import { onUnmounted, ref, shallowRef } from "vue";
import { Syncwell } from "syncwell-client";
import { Awareness } from "syncwell-client/awareness";

export function useSyncwell({ url, room, token, clientId, user, persist = true }) {
  const state = shallowRef({});
  const status = ref("connecting");
  const peers = ref([]);
  const canUndo = ref(false);
  const canRedo = ref(false);

  const sw = new Syncwell(url, room, { token, clientId, persist });
  const awareness = new Awareness(sw, { user: user || { id: clientId || sw.clientId }, start: false });
  const sync = () => {
    state.value = sw.live();
    canUndo.value = sw.canUndo();
    canRedo.value = sw.canRedo();
  };
  const onOpen = () => {
    status.value = "live";
    awareness.start();
  };

  sw.on("change", sync);
  sw.on("history", sync);
  sw.on("open", onOpen);
  sw.on("close", () => (status.value = "offline"));
  awareness.on("update", (next) => (peers.value = next));
  sync();
  sw.ready.then(() => sw.connect());

  onUnmounted(() => {
    awareness.close();
    sw.off("change", sync);
    sw.off("history", sync);
    sw.off("open", onOpen);
    sw.close();
  });

  return {
    client: sw,
    state,
    status,
    peers,
    canUndo,
    canRedo,
    set: (key, value) => sw.set(key, value),
    remove: (key) => sw.delete(key),
    undo: () => sw.undo(),
    redo: () => sw.redo(),
    cursor: (index, end = index) => awareness.setSelection(index, end),
  };
}
