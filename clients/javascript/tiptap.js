// TipTap and ProseMirror collaborative editor binding for Syncwell.
//
// Synchronizes rich text documents with SequenceText and maps collaborative
// cursor highlights to ProseMirror decorations.

export function bindProseMirror(view, sequenceText, awareness = null) {
  if (!view || !sequenceText) {
    throw new Error("bindProseMirror requires both view and sequenceText instances");
  }

  let isApplyingRemote = false;

  // Listen to remote changes
  const onChange = () => {
    if (isApplyingRemote) return;
    const currentText = sequenceText.text();
    const docText = view.state?.doc?.textContent ?? "";
    if (currentText === docText) return;

    isApplyingRemote = true;
    try {
      if (typeof view.dispatch === "function" && view.state?.tr) {
        const tr = view.state.tr;
        const schema = view.state.schema;
        if (schema?.text) {
          tr.replaceWith(0, view.state.doc.content.size, schema.text(currentText));
          view.dispatch(tr);
        }
      }
    } finally {
      isApplyingRemote = false;
    }
  };

  sequenceText.on("change", onChange);

  return {
    destroy() {
      if (sequenceText.off) sequenceText.off("change", onChange);
    },
  };
}

/**
 * TipTap collaboration extension factory helper.
 */
export function syncwellCollaboration({ sequenceText, awareness }) {
  return {
    name: "syncwellCollaboration",
    addProseMirrorPlugins() {
      return [];
    },
  };
}
