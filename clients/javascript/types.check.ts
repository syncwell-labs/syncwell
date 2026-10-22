// Compile-only checks for the hand-written declarations. Run with
// `npm run typecheck` (no output files are produced).
import { Syncwell, SyncwellError, protocolVersion } from "./syncwell.js";
import type { PresenceEvent, SyncwellOptions } from "./syncwell.js";
import { Clock, LWWMap, stableJSON, tsAfter } from "./crdt.js";
import type { Register, Timestamp } from "./crdt.js";
import { SequenceDoc } from "./sequence.js";
import { SequenceText, rankBetween } from "./text.js";
import { Awareness } from "./awareness.js";
import type { AwarenessPeer } from "./awareness.js";
import { MemoryStorage, IndexedDBStorage, indexedDBStorage } from "./storage.js";

type State = { title: string; count: number; cursor?: { x: number; y: number } };

const opts: SyncwellOptions = { token: "t", clientId: "user-1", persist: true };
const sw = new Syncwell<State>("wss://example.test/ws", "room", opts);

sw.on("change", (live) => {
  const title: string = live.title;
  void title.toUpperCase();
});
sw.on("presence", (event: PresenceEvent) => void event.data);
sw.on("error", (err: SyncwellError) => void err.code);

sw.connect().close();
sw.off("change");
sw.set("title", "hello");
sw.batch(() => {
  sw.set("title", "a");
  sw.set("count", 3);
});
void sw.undo();
void sw.redo();
void sw.canUndo();
void sw.canRedo();
sw.clearHistory();
void sw.ready;
sw.set("count", 2);
// @ts-expect-error count is a number, not a string
sw.set("count", "two");
// @ts-expect-error unknown key is rejected when State is typed
sw.set("missing", true);

const title: string | undefined = sw.get("title");
const live: State = sw.live();
void title;
void live;
void protocolVersion;
void sw.doc.snapshot();

const ts: Timestamp = { c: 1, n: "a" };
const reg: Register = { v: "x", t: ts };
void tsAfter(ts, ts);
void stableJSON({ b: 1, a: 2 });
void reg;

const clock = new Clock("node");
const map = new LWWMap<State>();
map.set("title", "x", clock.tick());
void map.get("title");

const seq = new SequenceDoc();
seq.insert("a", "", "h");
seq.insertRanked("b", "n", "i");
seq.merge(seq.snapshot());
void seq.text();

const storage = new MemoryStorage();
const idb = new IndexedDBStorage({ name: "syncwell-test" });
void indexedDBStorage();
const withStorage = new Syncwell<State>("wss://example.test/ws", "room", { storage, clientId: "u2" });
void withStorage.ready;
void idb.getItem("k");

const text = new SequenceText(sw, { prefix: "body:" });
text.on("change", (value: string) => value.toUpperCase());
text.insertAt(0, "hello");
text.deleteAt(1, 2);
text.replaceRange(0, 1, "H");
text.markRange(0, 1, { bold: true });
void text.text();
void text.length;
void text.marksAt(0);
void rankBetween("a", "b");

const awareness = new Awareness(sw, { user: { id: "u1", name: "Ada" } });
awareness.setCursor({ index: 1, selectionEnd: 3 });
awareness.setSelection(2);
awareness.on("update", (peers: AwarenessPeer[]) => void peers.length);
void awareness.getStates();
awareness.close();
