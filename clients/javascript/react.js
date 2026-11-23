// First-class React integration for Syncwell.
//
// Provides <SyncwellProvider> and React 18+ hooks:
//   useSyncwell, useStorage, usePresence, useOthers, useSelf, useMutation, useHistory
import * as React from "react";
import { Syncwell } from "./syncwell.js";
import { Awareness } from "./awareness.js";
import { CRDTArray } from "./array.js";

const SyncwellContext = React.createContext(null);

export function SyncwellProvider({
  client: externalClient,
  url,
  room = "default",
  options,
  children,
}) {
  const isManaged = !externalClient;

  const client = React.useMemo(() => {
    if (externalClient) return externalClient;
    if (!url) return null;
    return new Syncwell(url, room, options);
  }, [externalClient, url, room, options]);

  const awareness = React.useMemo(() => {
    if (!client) return null;
    return new Awareness(client, options?.awareness);
  }, [client, options?.awareness]);

  React.useEffect(() => {
    if (isManaged && client) {
      if (typeof window !== "undefined") {
        client.connect();
      }
      return () => {
        client.close();
      };
    }
  }, [isManaged, client]);

  React.useEffect(() => {
    return () => {
      awareness?.close();
    };
  }, [awareness]);

  const contextValue = React.useMemo(
    () => ({
      client,
      awareness,
    }),
    [client, awareness]
  );

  return React.createElement(SyncwellContext.Provider, { value: contextValue }, children);
}

export function useSyncwell() {
  const ctx = React.useContext(SyncwellContext);
  if (!ctx || !ctx.client) {
    throw new Error("useSyncwell must be used within a <SyncwellProvider>");
  }
  return ctx.client;
}

export function useAwareness() {
  const ctx = React.useContext(SyncwellContext);
  if (!ctx || !ctx.awareness) {
    throw new Error("useAwareness must be used within a <SyncwellProvider>");
  }
  return ctx.awareness;
}

export function useStorage(key, fallback = null) {
  const client = useSyncwell();
  const [value, setValue] = React.useState(() => {
    const current = client.get(key);
    return current !== undefined ? current : fallback;
  });

  React.useEffect(() => {
    const update = () => {
      const current = client.get(key);
      setValue(current !== undefined ? current : fallback);
    };
    client.on("change", update);
    return () => client.off("change", update);
  }, [client, key, fallback]);

  const setStorageValue = React.useCallback(
    (nextValue) => {
      client.set(key, nextValue);
    },
    [client, key]
  );

  return [value, setStorageValue];
}

export function usePresence(initialState) {
  const awareness = useAwareness();
  const [presence, setPresence] = React.useState(() => {
    if (initialState) {
      awareness.update(initialState);
    }
    return { ...awareness.local, ...(initialState || {}) };
  });

  React.useEffect(() => {
    if (initialState) {
      awareness.update(initialState);
    }
    const handler = (states) => {
      const selfState = states.find((s) => s.self);
      if (selfState) setPresence(selfState);
    };
    awareness.on("update", handler);
    return () => awareness.off("update", handler);
  }, [awareness]);

  const updatePresence = React.useCallback(
    (partial) => {
      awareness.update(partial);
      setPresence((prev) => ({ ...prev, ...partial }));
    },
    [awareness]
  );

  return [presence, updatePresence];
}

/**
 * Subscribes to a collaborative, ordered CRDT array.
 */
export function useArray(prefix = "arr:") {
  const client = useSyncwell();
  const arr = React.useMemo(() => new CRDTArray(client, { prefix }), [client, prefix]);
  const [items, setItems] = React.useState(() => arr.toArray());

  React.useEffect(() => {
    const update = (newItems) => setItems(newItems);
    arr.on("change", update);
    return () => {
      arr.destroy();
    };
  }, [arr]);

  return [items, arr];
}

export function useOthers() {
  const awareness = useAwareness();
  const [others, setOthers] = React.useState(() => {
    return awareness.getStates().filter((s) => !s.self);
  });

  React.useEffect(() => {
    const update = () => {
      setOthers(awareness.getStates().filter((s) => !s.self));
    };
    awareness.on("update", update);
    return () => awareness.off("update", update);
  }, [awareness]);

  return others;
}

export function useSelf() {
  const client = useSyncwell();
  const awareness = useAwareness();

  const getLocal = React.useCallback(
    () => ({
      clientId: client.clientId,
      self: true,
      connected: typeof client._connected === "function" ? client._connected() : false,
      ...awareness.local,
    }),
    [client, awareness]
  );

  const [self, setSelf] = React.useState(() => getLocal());

  React.useEffect(() => {
    const update = () => setSelf(getLocal());
    awareness.on("update", update);
    client.on("open", update);
    client.on("close", update);
    return () => {
      awareness.off("update", update);
      client.off("open", update);
      client.off("close", update);
    };
  }, [client, awareness, getLocal]);

  return self;
}

export function useMutation(fn) {
  const client = useSyncwell();
  return React.useCallback(
    (...args) => {
      let result;
      client.batch(() => {
        const ctx = {
          set: (k, v) => client.set(k, v),
          delete: (k) => client.delete(k),
          get: (k) => client.get(k),
          batch: (inner) => client.batch(inner),
        };
        result = fn(ctx, ...args);
      });
      return result;
    },
    [client, fn]
  );
}

export function useHistory() {
  const client = useSyncwell();
  const [history, setHistory] = React.useState(() => ({
    canUndo: typeof client.canUndo === "function" ? client.canUndo() : false,
    canRedo: typeof client.canRedo === "function" ? client.canRedo() : false,
  }));

  React.useEffect(() => {
    const update = (h) => {
      setHistory({
        canUndo: h?.canUndo ?? (typeof client.canUndo === "function" ? client.canUndo() : false),
        canRedo: h?.canRedo ?? (typeof client.canRedo === "function" ? client.canRedo() : false),
      });
    };
    client.on("history", update);
    return () => client.off("history", update);
  }, [client]);

  const undo = React.useCallback(() => client.undo(), [client]);
  const redo = React.useCallback(() => client.redo(), [client]);

  return {
    undo,
    redo,
    canUndo: history.canUndo,
    canRedo: history.canRedo,
  };
}

const CURSOR_COLORS = [
  "#E57373", "#81C784", "#64B5F6", "#FFB74D",
  "#BA68C8", "#4DD0E1", "#AED581", "#FF8A65",
  "#F06292", "#4DB6AC"
];

function stringToColor(str) {
  if (!str) return CURSOR_COLORS[0];
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = (hash << 5) - hash + str.charCodeAt(i);
    hash |= 0;
  }
  return CURSOR_COLORS[Math.abs(hash) % CURSOR_COLORS.length];
}

/**
 * Drop-in multiplayer live cursors overlay component.
 */
export function LiveCursors({
  containerRef,
  propagate = true,
  renderCursor,
  className,
  style,
}) {
  const others = useOthers();
  const [, updatePresence] = usePresence();

  React.useEffect(() => {
    if (!propagate || typeof window === "undefined") return;

    const target = containerRef ? containerRef.current : window;
    if (!target) return;

    const handlePointerMove = (e) => {
      let x = e.clientX;
      let y = e.clientY;
      if (containerRef && containerRef.current) {
        const rect = containerRef.current.getBoundingClientRect();
        x = e.clientX - rect.left;
        y = e.clientY - rect.top;
      }
      updatePresence({ cursor: { x, y } });
    };

    const handlePointerLeave = () => {
      updatePresence({ cursor: null });
    };

    target.addEventListener("pointermove", handlePointerMove);
    target.addEventListener("pointerleave", handlePointerLeave);
    return () => {
      target.removeEventListener("pointermove", handlePointerMove);
      target.removeEventListener("pointerleave", handlePointerLeave);
    };
  }, [containerRef, propagate, updatePresence]);

  const peersWithCursor = others.filter(
    (p) => p.cursor && typeof p.cursor.x === "number" && typeof p.cursor.y === "number"
  );

  return React.createElement(
    "div",
    {
      className: className ? `syncwell-live-cursors ${className}` : "syncwell-live-cursors",
      style: {
        position: containerRef ? "absolute" : "fixed",
        top: 0,
        left: 0,
        width: "100%",
        height: "100%",
        pointerEvents: "none",
        zIndex: 9999,
        overflow: "hidden",
        ...style,
      },
    },
    peersWithCursor.map((peer) => {
      const color = peer.user?.color || stringToColor(peer.clientId);
      const name = peer.user?.name || (peer.clientId ? peer.clientId.slice(0, 6) : "Peer");

      if (renderCursor) {
        return renderCursor({ peer, color, name, key: peer.clientId });
      }

      return React.createElement(
        "div",
        {
          key: peer.clientId,
          style: {
            position: "absolute",
            left: `${peer.cursor.x}px`,
            top: `${peer.cursor.y}px`,
            transition: "transform 0.08s ease-out, left 0.08s ease-out, top 0.08s ease-out",
            transform: "translate(0, 0)",
            pointerEvents: "none",
          },
        },
        React.createElement(
          "svg",
          {
            width: 24,
            height: 24,
            viewBox: "0 0 24 24",
            fill: "none",
            stroke: "white",
            strokeWidth: "1.5",
            strokeLinecap: "round",
            strokeLinejoin: "round",
            style: { display: "block", filter: "drop-shadow(0 2px 4px rgba(0,0,0,0.2))" },
          },
          React.createElement("polygon", {
            points: "0 0, 0 18, 5 14, 8 21, 11 20, 8 13, 14 13",
            fill: color,
          })
        ),
        React.createElement(
          "div",
          {
            style: {
              position: "absolute",
              left: 14,
              top: 14,
              backgroundColor: color,
              color: "#ffffff",
              fontSize: "11px",
              fontWeight: "600",
              fontFamily: "system-ui, -apple-system, sans-serif",
              padding: "2px 6px",
              borderRadius: "4px",
              whiteSpace: "nowrap",
              boxShadow: "0 2px 4px rgba(0,0,0,0.2)",
            },
          },
          name
        )
      );
    })
  );
}

/**
 * Drop-in avatar stack for active room peers.
 */
export function LiveAvatars({
  limit = 5,
  size = 32,
  includeSelf = true,
  className,
  style,
  renderAvatar,
}) {
  const others = useOthers();
  const self = useSelf();

  const allUsers = React.useMemo(() => {
    const list = [];
    if (includeSelf && self) {
      list.push(self);
    }
    list.push(...others);
    return list;
  }, [includeSelf, self, others]);

  const visible = allUsers.slice(0, limit);
  const extraCount = allUsers.length - limit;

  return React.createElement(
    "div",
    {
      className: className ? `syncwell-live-avatars ${className}` : "syncwell-live-avatars",
      style: {
        display: "inline-flex",
        alignItems: "center",
        flexDirection: "row",
        ...style,
      },
    },
    visible.map((user, idx) => {
      const color = user.user?.color || stringToColor(user.clientId);
      const name = user.user?.name || (user.self ? "You" : user.clientId ? user.clientId.slice(0, 5) : "User");
      const avatarUrl = user.user?.avatar;
      const initials = name ? name.slice(0, 2).toUpperCase() : "U";

      if (renderAvatar) {
        return renderAvatar({ user, color, name, initials, index: idx });
      }

      return React.createElement(
        "div",
        {
          key: user.clientId || (user.self ? "self" : idx),
          title: user.self ? `${name} (You)` : name,
          style: {
            width: `${size}px`,
            height: `${size}px`,
            borderRadius: "50%",
            backgroundColor: color,
            border: "2px solid #ffffff",
            marginLeft: idx === 0 ? "0px" : `-${Math.round(size * 0.25)}px`,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            color: "#ffffff",
            fontSize: `${Math.max(10, Math.round(size * 0.38))}px`,
            fontWeight: "bold",
            fontFamily: "system-ui, -apple-system, sans-serif",
            overflow: "hidden",
            position: "relative",
            boxShadow: "0 1px 3px rgba(0,0,0,0.12)",
            flexShrink: 0,
            cursor: "default",
          },
        },
        avatarUrl
          ? React.createElement("img", {
              src: avatarUrl,
              alt: name,
              style: { width: "100%", height: "100%", objectFit: "cover" },
            })
          : initials
      );
    }),
    extraCount > 0 &&
      React.createElement(
        "div",
        {
          key: "extra",
          title: `${extraCount} more active peer${extraCount > 1 ? "s" : ""}`,
          style: {
            width: `${size}px`,
            height: `${size}px`,
            borderRadius: "50%",
            backgroundColor: "#718096",
            border: "2px solid #ffffff",
            marginLeft: `-${Math.round(size * 0.25)}px`,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            color: "#ffffff",
            fontSize: `${Math.max(9, Math.round(size * 0.35))}px`,
            fontWeight: "bold",
            fontFamily: "system-ui, -apple-system, sans-serif",
            boxShadow: "0 1px 3px rgba(0,0,0,0.12)",
            flexShrink: 0,
            cursor: "default",
          },
        },
        `+${extraCount}`
      )
  );
}
