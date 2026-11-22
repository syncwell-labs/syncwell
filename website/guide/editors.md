# Editor Adapters (Monaco & ProseMirror/TipTap)

Syncwell includes zero-dependency collaborative editor adapters to bind popular text engines directly to Syncwell's `SequenceText` and `Awareness` CRDT engine.

## Monaco Editor (`syncwell-client/monaco`)

Turn any Monaco editor instance into a real-time collaborative code editor with shared cursor decorations and live selections:

```js
import * as monaco from "monaco-editor";
import { Syncwell } from "syncwell-client";
import { SequenceText } from "syncwell-client/text";
import { Awareness } from "syncwell-client/awareness";
import { bindMonaco } from "syncwell-client/monaco";

const sw = new Syncwell("wss://sync.example.com/ws", "code-room");
const text = new SequenceText(sw, { prefix: "code:" });
const awareness = new Awareness(sw, { user: { name: "Alice" } });

const editor = monaco.editor.create(document.getElementById("editor"), {
  value: "",
  language: "typescript",
});

// Bind Monaco to Syncwell:
const binding = bindMonaco(editor, text, awareness);

// When unmounting / disposing:
binding.destroy();
```

## ProseMirror & TipTap (`syncwell-client/tiptap`)

Bind ProseMirror views or TipTap editors:

```js
import { EditorView } from "prosemirror-view";
import { bindProseMirror } from "syncwell-client/tiptap";

const binding = bindProseMirror(view, sequenceText, awareness);

// Cleanup
binding.destroy();
```
