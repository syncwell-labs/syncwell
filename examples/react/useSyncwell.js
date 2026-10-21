// React hook: one room, one local replica, typed document state.
//
//   const { state, set, undo, redo, status } = useSyncwell({
//     url: "wss://sync.example/ws", room: "notes", clientId: "u1",
//   });
//
// Install: npm i react syncwell-client
import { useCallback, useEffect, useRef, useState } from "react";
import { Syncwell } from "syncwell-client";
import { Awareness } from "syncwell-client/awareness";

export function useSyncwell({ url, room, token, clientId, user, persist = true }) {
  const userName = user?.name;
  const userColor = user?.color;
  const clientRef = useRef(null);
  const [state, setState] = useState({});
  const [status, setStatus] = useState("connecting");
  const [peers, setPeers] = useState([]);
  const [history, setHistory] = useState({ canUndo: false, canRedo: false });

  useEffect(() => {
    const sw = new Syncwell(url, room, { token, clientId, persist });
    const awareness = new Awareness(sw, {
      user: { id: user?.id || clientId || sw.clientId, name: userName, color: userColor },
      start: false,
    });
    clientRef.current = { sw, awareness };

    const offChange = () => setState(sw.live());
    sw.on("change", offChange);
    sw.on("history", setHistory);
    sw.on("open", () => {
      setStatus("live");
      awareness.start();
    });
    sw.on("close", () => setStatus("offline"));
    sw.on("error", () => setStatus("error"));
    awareness.on("update", setPeers);
    sw.ready.then(() => {
      setState(sw.live());
      setHistory({ canUndo: sw.canUndo(), canRedo: sw.canRedo() });
    });
    sw.connect();

    return () => {
      awareness.close();
      sw.off("change", offChange);
      sw.close();
      clientRef.current = null;
    };
  }, [url, room, token, clientId, userName, userColor, persist]);

  const set = useCallback((key, value) => clientRef.current?.sw.set(key, value), []);
  const remove = useCallback((key) => clientRef.current?.sw.delete(key), []);
  const undo = useCallback(() => clientRef.current?.sw.undo(), []);
  const redo = useCallback(() => clientRef.current?.sw.redo(), []);
  const cursor = useCallback((index, selectionEnd = index) => clientRef.current?.awareness.setSelection(index, selectionEnd), []);

  return { state, status, peers, history, set, remove, undo, redo, cursor, client: clientRef };
}
