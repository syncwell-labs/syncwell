# React Integration (`syncwell-client/react`)

Syncwell provides first-class, SSR-safe React 18+ hooks and context for building collaborative user interfaces.

## Installation

```bash
npm install syncwell-client react
```

## `<SyncwellProvider>`

Wrap your component tree in `<SyncwellProvider>`. You can pass a server URL, room ID, and options, or pass an existing `client` instance.

```tsx
import React from "react";
import { SyncwellProvider } from "syncwell-client/react";

export function App() {
  return (
    <SyncwellProvider url="wss://sync.example.com/ws" room="project-collab">
      <CollabWorkspace />
    </SyncwellProvider>
  );
}
```

## Core Hooks

### `useStorage(key, fallback)`
Subscribes reactively to a specific key in the room's CRDT document. Re-renders only when that key updates.

```tsx
import { useStorage } from "syncwell-client/react";

function Counter() {
  const count = useStorage("count", 0);
  return <div>Current count: {count}</div>;
}
```

### `useMutation(fn)`
Executes atomic, multi-key writes wrapped in a single undo step and network broadcast.

```tsx
import { useMutation } from "syncwell-client/react";

function Controls() {
  const increment = useMutation(({ set, get }) => {
    const cur = get("count") || 0;
    set("count", cur + 1);
  });

  return <button onClick={() => increment()}>+1</button>;
}
```

### `usePresence(initialState)`
Publishes ephemeral presence state (such as user status, selections, or cursor positions) and returns the current state and an updater.

```tsx
import { usePresence } from "syncwell-client/react";

function UserStatus() {
  const [presence, updatePresence] = usePresence({ status: "active" });

  return (
    <button onClick={() => updatePresence({ status: "away" })}>
      Status: {presence.status}
    </button>
  );
}
```

### `useOthers()`
Returns a reactive list of remote peers currently in the room.

```tsx
import { useOthers } from "syncwell-client/react";

function PeerAvatars() {
  const others = useOthers();

  return (
    <div className="flex gap-2">
      {others.map((peer) => (
        <span key={peer.clientId} style={{ color: peer.color }}>
          {peer.user?.name || peer.clientId}
        </span>
      ))}
    </div>
  );
}
```

### `useSelf()`
Returns the local user's identity, presence, and WebSocket connection status.

```tsx
import { useSelf } from "syncwell-client/react";

function ConnectionBadge() {
  const self = useSelf();
  return <div>{self.connected ? "Connected" : "Reconnecting..."}</div>;
}
```

### `useHistory()`
Provides reactive undo and redo handlers.

```tsx
import { useHistory } from "syncwell-client/react";

function Toolbar() {
  const { undo, redo, canUndo, canRedo } = useHistory();

  return (
    <div>
      <button onClick={undo} disabled={!canUndo}>Undo</button>
      <button onClick={redo} disabled={!canRedo}>Redo</button>
    </div>
  );
}
```

## Ready-to-Use Multiplayer UI Components

Syncwell includes drop-in, zero-dependency multiplayer components to get Google-Docs or Figma-style collaboration in minutes without writing custom CSS or SVG math.

### `<LiveCursors />`

Renders smooth, color-coded live pointer arrows for all active peers, complete with name badges and optional container containment:

```tsx
import { SyncwellProvider, LiveCursors } from "syncwell-client/react";

export function App() {
  return (
    <SyncwellProvider url="wss://sync.example.com/ws" room="design-board">
      <div style={{ position: "relative", minHeight: "100vh" }}>
        <h1>Collaborative Whiteboard</h1>
        {/* Renders all remote user cursors automatically */}
        <LiveCursors />
      </div>
    </SyncwellProvider>
  );
}
```

#### Props

| Prop | Type | Default | Description |
| --- | --- | --- | --- |
| `containerRef` | `React.RefObject` | `undefined` (window) | Confine cursor coordinates to a specific DOM element. |
| `propagate` | `boolean` | `true` | Automatically track and broadcast the local pointer position. |
| `className` | `string` | `""` | Optional CSS class name for the wrapper. |
| `style` | `React.CSSProperties` | `{}` | Optional CSS style overrides. |
| `renderCursor` | `Function` | Default SVG arrow | Custom render prop `({ peer, color, name, key }) => ReactNode`. |

### `<LiveAvatars />`

Displays an overlapping avatar stack representing all users currently active in the room:

```tsx
import { LiveAvatars } from "syncwell-client/react";

export function Header() {
  return (
    <header className="header">
      <h2>Project Dashboard</h2>
      <LiveAvatars limit={4} size={32} />
    </header>
  );
}
```

#### Props

| Prop | Type | Default | Description |
| --- | --- | --- | --- |
| `limit` | `number` | `5` | Maximum number of avatars shown before displaying `+N`. |
| `size` | `number` | `32` | Diameter of each circular avatar in pixels. |
| `includeSelf` | `boolean` | `true` | Whether to include the current user in the avatar list. |
| `renderAvatar` | `Function` | Initials / photo | Custom avatar render prop `({ user, color, name, initials, index }) => ReactNode`. |

