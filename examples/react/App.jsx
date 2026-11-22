import React from "react";
import {
  SyncwellProvider,
  useStorage,
  useArray,
  useHistory,
  LiveCursors,
  LiveAvatars,
} from "syncwell-client/react";

function Workspace() {
  const [title, setTitle] = useStorage("doc:title", "Multiplayer Project");
  const [tasks, taskList] = useArray("tasks:");
  const { undo, redo, canUndo, canRedo } = useHistory();
  const [newTaskText, setNewTaskText] = React.useState("");

  const handleAddTask = (e) => {
    e.preventDefault();
    if (!newTaskText.trim()) return;
    taskList.push({ text: newTaskText.trim(), done: false, createdAt: Date.now() });
    setNewTaskText("");
  };

  const handleMove = (fromIdx, toIdx) => {
    taskList.move(fromIdx, toIdx);
  };

  const handleDelete = (idx) => {
    taskList.delete(idx);
  };

  const handleToggle = (idx) => {
    const item = tasks[idx];
    if (!item) return;
    // Update value while keeping position
    const id = taskList.getId(idx);
    if (id) {
      taskList.sw.set(taskList.prefix + id, {
        id,
        rank: taskList._nodes()[idx]?.rank,
        value: { ...item, done: !item.done },
      });
    }
  };

  return (
    <div className="workspace-container" style={{ position: "relative", minHeight: "100vh", padding: "2rem", fontFamily: "system-ui, sans-serif" }}>
      {/* 1. Live Cursors Overlay */}
      <LiveCursors />

      {/* 2. Top Header with Live Avatars & Controls */}
      <header style={{ display: "flex", justifyContent: "space-between", alignItems: "center", borderBottom: "1px solid #e2e8f0", paddingBottom: "1rem", marginBottom: "2rem" }}>
        <div>
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            style={{ fontSize: "1.75rem", fontWeight: "bold", border: "none", outline: "none", background: "transparent" }}
            placeholder="Untitled Document"
          />
          <div style={{ color: "#718096", fontSize: "0.875rem", marginTop: "4px" }}>
            Press <kbd style={{ background: "#edf2f7", padding: "2px 6px", borderRadius: "4px" }}>Ctrl+Shift+S</kbd> to inspect CRDT DevTools
          </div>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: "1rem" }}>
          {/* History Undo/Redo */}
          <div style={{ display: "flex", gap: "4px" }}>
            <button onClick={undo} disabled={!canUndo} style={{ padding: "6px 12px", borderRadius: "6px", border: "1px solid #cbd5e0", background: canUndo ? "#fff" : "#f7fafc", cursor: canUndo ? "pointer" : "not-allowed" }}>
              ↩ Undo
            </button>
            <button onClick={redo} disabled={!canRedo} style={{ padding: "6px 12px", borderRadius: "6px", border: "1px solid #cbd5e0", background: canRedo ? "#fff" : "#f7fafc", cursor: canRedo ? "pointer" : "not-allowed" }}>
              ↪ Redo
            </button>
          </div>

          {/* Overlapping active user avatars */}
          <LiveAvatars limit={5} size={36} />
        </div>
      </header>

      {/* 3. Collaborative Task List */}
      <main style={{ maxWidth: "600px", margin: "0 auto" }}>
        <form onSubmit={handleAddTask} style={{ display: "flex", gap: "8px", marginBottom: "1.5rem" }}>
          <input
            value={newTaskText}
            onChange={(e) => setNewTaskText(e.target.value)}
            placeholder="Add a new collaborative task..."
            style={{ flex: 1, padding: "10px 14px", borderRadius: "8px", border: "1px solid #cbd5e0", fontSize: "1rem" }}
          />
          <button type="submit" style={{ padding: "10px 20px", borderRadius: "8px", background: "#3182ce", color: "#fff", border: "none", fontWeight: "600", cursor: "pointer" }}>
            Add
          </button>
        </form>

        <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
          {tasks.length === 0 && (
            <div style={{ textAlign: "center", color: "#a0aec0", padding: "2rem" }}>No tasks yet. Add one above!</div>
          )}

          {tasks.map((task, idx) => (
            <div
              key={taskList.getId(idx) || idx}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                padding: "12px 16px",
                borderRadius: "8px",
                background: "#ffffff",
                border: "1px solid #e2e8f0",
                boxShadow: "0 1px 3px rgba(0,0,0,0.05)",
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
                <input
                  type="checkbox"
                  checked={Boolean(task.done)}
                  onChange={() => handleToggle(idx)}
                  style={{ width: "18px", height: "18px", cursor: "pointer" }}
                />
                <span style={{ textDecoration: task.done ? "line-through" : "none", color: task.done ? "#a0aec0" : "#2d3748" }}>
                  {task.text}
                </span>
              </div>

              <div style={{ display: "flex", gap: "6px" }}>
                <button
                  onClick={() => handleMove(idx, idx - 1)}
                  disabled={idx === 0}
                  style={{ padding: "4px 8px", borderRadius: "4px", border: "1px solid #e2e8f0", background: "#f7fafc", cursor: idx === 0 ? "default" : "pointer" }}
                  title="Move Up"
                >
                  ↑
                </button>
                <button
                  onClick={() => handleMove(idx, idx + 1)}
                  disabled={idx === tasks.length - 1}
                  style={{ padding: "4px 8px", borderRadius: "4px", border: "1px solid #e2e8f0", background: "#f7fafc", cursor: idx === tasks.length - 1 ? "default" : "pointer" }}
                  title="Move Down"
                >
                  ↓
                </button>
                <button
                  onClick={() => handleDelete(idx)}
                  style={{ padding: "4px 8px", borderRadius: "4px", border: "none", background: "#fed7d7", color: "#9b2c2c", cursor: "pointer" }}
                  title="Delete"
                >
                  ✕
                </button>
              </div>
            </div>
          ))}
        </div>
      </main>
    </div>
  );
}

export default function App() {
  const room = new URLSearchParams(window.location.search).get("room") || "demo-room";

  return (
    <SyncwellProvider url="ws://localhost:8080/ws" room={room}>
      <Workspace />
    </SyncwellProvider>
  );
}
