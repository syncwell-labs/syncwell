# React 18+ Multiplayer Example

This example demonstrates complete real-time collaboration using `syncwell-client/react`:
- **Live Cursors (`<LiveCursors />`)**: Tracks and animates remote user cursors.
- **Active User Avatars (`<LiveAvatars />`)**: Overlapping user badge stack.
- **Collaborative Lists (`useArray`)**: Ordered task list with real-time insertion, deletion, and drag/move re-ordering.
- **Conflict-Free Document State (`useStorage`)**: Real-time collaborative document title.
- **Grouped History (`useHistory`)**: Coordinated Undo/Redo across participants.

## Quickstart

1. Build the JavaScript SDK from the root of the repository:
   ```bash
   cd ../../clients/javascript && npm run build
   ```

2. Start the local Syncwell Go server:
   ```bash
   go run ./cmd/syncwell -addr 127.0.0.1:8080
   ```

3. Open `App.jsx` in your React project (e.g., Vite or Next.js):
   ```bash
   npm install syncwell-client react react-dom
   ```

4. Open `http://localhost:5173/?room=demo` in two side-by-side browser windows to test live cursor tracking, instant task sync, and undo/redo!
