# Text and awareness

Imports assume `syncwell-client` from npm — until it is published, copy `clients/javascript/`
or import from your server's `/sdk/` mount (see [JavaScript SDK](/guide/sdk)).

## Collaborative text

```js
import { SequenceText } from "syncwell-client/text";

const text = new SequenceText(sw, { prefix: "body:" });

text.insertAt(0, "Hello");
text.replaceRange(5, 0, ", world");
text.deleteAt(0, 1);
text.markRange(0, 5, { bold: true });

text.on("change", () => render(text.text()));
```

Offsets are Unicode code points. Every operation is one batch, so a replace or a
formatting action is one undo step.

| Method | Description |
| --- | --- |
| `insertAt(index, value)` | Insert text, returns inserted element ids |
| `deleteAt(index, count)` | Tombstone `count` characters |
| `replaceRange(index, count, value)` | Delete then insert in one batch |
| `markRange(index, count, marks)` | Apply rich-text marks; `null` clears |
| `text()` / `visibleIds()` | Visible text / element ids |
| `marksAt(index)` | Marks at a visible offset |
| `length` | Visible code-point count |
| `close()` | Detach the `Syncwell` change listener |

Marks are simple per-character metadata (`{ bold: true }`), not a full rich-text
document model. Store block structure in normal map keys.

## Typed awareness

```js
import { Awareness } from "syncwell-client/awareness";

const awareness = new Awareness(sw, {
  user: { id: "u1", name: "Ada", color: "#a03a22" },
  heartbeat: 10_000,
  timeout: 30_000,
});

awareness.setSelection(12, 18);
awareness.setCursor({ index: 18 });

awareness.on("update", (peers) => {
  renderCursors(peers.filter((peer) => !peer.self));
});
```

`Awareness` republishes on reconnect. Presence is sent once the first
snapshot/delta arrives, so an initial cursor is not lost while the socket is
still handshaking.

Events: `update`, `join`, `change`, `remove`, `error`.
