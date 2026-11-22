# Collaborative Lists (`CRDTArray` & `useArray`)

Syncwell provides a conflict-free ordered collection CRDT (`CRDTArray`) built on fractional indexing. It allows multiple users to insert, delete, and re-order items (such as Kanban cards, Todo items, or Figma layers) concurrently without duplicates, lost updates, or array index clobbering.

## Installation

```bash
npm install syncwell-client
```

## Vanilla JavaScript / Node

```js
import { Syncwell } from "syncwell-client";
import { CRDTArray } from "syncwell-client/array";

const sw = new Syncwell("wss://sync.example.com/ws", "kanban-room");
const list = new CRDTArray(sw, { prefix: "tasks:" });

// Push items to the end of the array
list.push({ title: "Design mockup" }, { title: "Implement API" });

// Insert an item at index 0
list.insert(0, { title: "Kickoff meeting" });

// Move an item from index 2 to index 0 (drag-and-drop re-order)
list.move(2, 0);

// Delete an item at index 1
list.delete(1);

// Subscribe to reactive updates
list.on("change", (items) => {
  console.log("Updated list:", items);
});

// Access current items
console.log(list.toArray());
console.log(list.length);
```

## React 18+ Integration (`useArray`)

In React, the `useArray` hook gives you a reactive state array and an array controller in a single line:

```tsx
import React from "react";
import { SyncwellProvider, useArray } from "syncwell-client/react";

function KanbanColumn() {
  const [tasks, taskList] = useArray("kanban:todo:");

  const handleAddTask = () => {
    taskList.push({ id: Date.now(), text: "New Task" });
  };

  const handleMoveUp = (index: number) => {
    if (index > 0) {
      taskList.move(index, index - 1);
    }
  };

  const handleDelete = (index: number) => {
    taskList.delete(index);
  };

  return (
    <div className="kanban-column">
      <h3>To Do ({tasks.length})</h3>
      <button onClick={handleAddTask}>+ Add Task</button>

      <ul>
        {tasks.map((task, idx) => (
          <li key={taskList.getId(idx) || idx}>
            <span>{task.text}</span>
            <button onClick={() => handleMoveUp(idx)} disabled={idx === 0}>
              ↑
            </button>
            <button onClick={() => handleDelete(idx)}>✕</button>
          </li>
        ))}
      </ul>
    </div>
  );
}

export function App() {
  return (
    <SyncwellProvider url="wss://sync.example.com/ws" room="kanban-board">
      <KanbanColumn />
    </SyncwellProvider>
  );
}
```

## Why Traditional Arrays Fail in Collaboration

When two users edit standard JSON arrays concurrently:
- If User A deletes item at index `1` while User B edits item at index `1`, User B accidentally modifies the wrong item or crashes with an out-of-bounds error.
- If two users re-order items, one user's write completely overwrites the whole array, wiping out the other user's changes.

### How Syncwell Solves This
1. Each item is assigned an immutable unique ID and a **fractional rank** (e.g. `"m"`, `"n"`, `"na"`).
2. Moving an item computes a new rank strictly between its new neighbors (`rankBetween(left, right)`).
3. If two users re-order different items at the exact same moment, both moves succeed cleanly without conflicts.
4. If two users re-order the *same* item concurrently, Syncwell's Lamport clock deterministically tie-breaks using standard Last-Writer-Wins rules.
