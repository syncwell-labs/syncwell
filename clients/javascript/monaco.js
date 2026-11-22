// Monaco Editor (VS Code editor engine) collaborative binding for Syncwell.
//
// Automatically synchronizes Monaco editor content with SequenceText and maps
// multi-user awareness cursors and selections to Monaco editor decorations.
//
// Usage:
//   import { bindMonaco } from "syncwell-client/monaco";
//   const binding = bindMonaco(editor, sequenceText, awareness);
//   // when unmounting:
//   binding.destroy();

export function bindMonaco(editor, sequenceText, awareness = null) {
  if (!editor || !sequenceText) {
    throw new Error("bindMonaco requires both editor and sequenceText instances");
  }

  const model = editor.getModel ? editor.getModel() : editor;
  let isApplyingRemote = false;

  // Initialize editor text from CRDT document if editor is empty
  const initialText = sequenceText.text();
  if (model.getValue() !== initialText) {
    model.setValue(initialText);
  }

  // 1. Local editor changes -> Syncwell SequenceText
  const contentListener = model.onDidChangeContent((event) => {
    if (isApplyingRemote) return;

    for (const change of event.changes) {
      const offset = change.rangeOffset;
      const length = change.rangeLength;
      const newText = change.text;

      if (length > 0 && newText.length > 0) {
        sequenceText.replaceRange(offset, length, newText);
      } else if (length > 0) {
        sequenceText.deleteAt(offset, length);
      } else if (newText.length > 0) {
        sequenceText.insertAt(offset, newText);
      }
    }
  });

  // 2. Remote Syncwell text changes -> Monaco editor
  const textListener = () => {
    const currentEditorText = model.getValue();
    const crdtText = sequenceText.text();
    if (currentEditorText === crdtText) return;

    isApplyingRemote = true;
    try {
      const fullRange = model.getFullModelRange();
      model.pushEditOperations([], [{ range: fullRange, text: crdtText }], () => null);
    } finally {
      isApplyingRemote = false;
    }
  };
  sequenceText.on("change", textListener);

  // 3. Presence / remote cursors integration
  let cursorListener = null;
  let awarenessListener = null;
  let decorationIds = [];

  if (awareness && typeof editor.onDidChangeCursorSelection === "function") {
    // Send local cursor to awareness
    cursorListener = editor.onDidChangeCursorSelection((e) => {
      const sel = e.selection;
      const start = model.getOffsetAt(sel.getStartPosition());
      const end = model.getOffsetAt(sel.getEndPosition());
      awareness.setCursor({ index: start, selectionEnd: end });
    });

    // Render remote cursors as Monaco decorations
    awarenessListener = (peers) => {
      const newDecorations = [];
      for (const peer of peers) {
        if (peer.self || !peer.cursor) continue;
        const startPos = model.getPositionAt(peer.cursor.index);
        const endPos = model.getPositionAt(peer.cursor.selectionEnd || peer.cursor.index);

        const color = peer.color || "#0284c7";
        const name = peer.user?.name || peer.clientId || "Anonymous";

        newDecorations.push({
          range: {
            startLineNumber: startPos.lineNumber,
            startColumn: startPos.column,
            endLineNumber: endPos.lineNumber,
            endColumn: endPos.column,
          },
          options: {
            className: "syncwell-remote-selection",
            hoverMessage: { value: `**${name}**` },
            overviewRuler: {
              color,
              position: 7, // OverviewRulerLane.Full
            },
          },
        });
      }
      decorationIds = editor.deltaDecorations ? editor.deltaDecorations(decorationIds, newDecorations) : [];
    };
    awareness.on("update", awarenessListener);
  }

  return {
    destroy() {
      if (contentListener?.dispose) contentListener.dispose();
      if (cursorListener?.dispose) cursorListener.dispose();
      if (sequenceText.off) sequenceText.off("change", textListener);
      if (awareness && awarenessListener) awareness.off("update", awarenessListener);
      if (editor.deltaDecorations && decorationIds.length) {
        editor.deltaDecorations(decorationIds, []);
      }
    },
  };
}
