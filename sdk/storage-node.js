// Node-only file persistence adapter for the Syncwell SDK.
//
//   import { fileStorage } from "syncwell-client/storage-node";
//   const sw = new Syncwell(url, "room", {
//     clientId: "server-1",
//     storage: fileStorage("./.syncwell/cache.json"),
//   });
//
// The adapter keeps a small JSON map of storage keys. Writes are atomic
// (write + rename) so a crash cannot leave a half-written cache behind.
import { readFileSync, writeFileSync, renameSync, mkdirSync, existsSync, rmSync } from "node:fs";
import { dirname, resolve } from "node:path";

export class FileStorage {
  constructor(file) {
    this.file = resolve(file);
    this._items = null;
  }

  _load() {
    if (this._items) return this._items;
    if (!existsSync(this.file)) {
      this._items = {};
      return this._items;
    }
    try {
      this._items = JSON.parse(readFileSync(this.file, "utf8"));
      if (!this._items || typeof this._items !== "object") this._items = {};
    } catch {
      this._items = {};
    }
    return this._items;
  }

  _flush() {
    mkdirSync(dirname(this.file), { recursive: true });
    const tmp = `${this.file}.${process.pid}.tmp`;
    writeFileSync(tmp, JSON.stringify(this._items), { mode: 0o600 });
    renameSync(tmp, this.file);
  }

  getItem(key) {
    const items = this._load();
    return Object.prototype.hasOwnProperty.call(items, key) ? items[key] : null;
  }

  setItem(key, value) {
    const items = this._load();
    items[key] = String(value);
    this._flush();
  }

  removeItem(key) {
    const items = this._load();
    delete items[key];
    this._flush();
  }

  clear() {
    this._items = {};
    if (existsSync(this.file)) rmSync(this.file, { force: true });
  }
}

/** Return a file-backed storage adapter for the given JSON file path. */
export function fileStorage(file) {
  return new FileStorage(file);
}
