// Persistence adapters for the Syncwell SDK.
//
// `Syncwell` accepts any object with `getItem(key)` / `setItem(key, value)`.
// Values may be returned synchronously (localStorage, memory, Node file) or as
// a Promise (IndexedDB). Await `sw.ready` if you need the initial load done.
//
//   import { Syncwell } from "syncwell-client";
//   import { indexedDBStorage } from "syncwell-client/storage";
//   const storage = indexedDBStorage("my-app");
//   const sw = new Syncwell(url, "room", { storage, clientId: "u1" });
//   await sw.ready;

/** In-memory adapter, useful for tests and sandboxed environments. */
export class MemoryStorage {
  constructor(initial = {}) {
    this.items = new Map(Object.entries(initial));
  }
  getItem(key) {
    return this.items.has(key) ? this.items.get(key) : null;
  }
  setItem(key, value) {
    this.items.set(key, String(value));
  }
  removeItem(key) {
    this.items.delete(key);
  }
  clear() {
    this.items.clear();
  }
}

/** Thin wrapper that makes the adapter contract explicit. */
export class LocalStorageAdapter {
  constructor(storage = globalThis.localStorage) {
    if (!storage) throw new Error("localStorage is not available in this environment");
    this.storage = storage;
  }
  getItem(key) {
    return this.storage.getItem(key);
  }
  setItem(key, value) {
    this.storage.setItem(key, String(value));
  }
  removeItem(key) {
    this.storage.removeItem(key);
  }
}

function requestToPromise(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error || new Error("IndexedDB request failed"));
  });
}

/**
 * IndexedDB-backed adapter. Provides much larger quota than localStorage and
 * never blocks the main thread; reads/writes are asynchronous, which Syncwell
 * supports natively.
 */
export class IndexedDBStorage {
  constructor({ name = "syncwell", store = "state", version = 1 } = {}) {
    if (typeof indexedDB === "undefined") {
      throw new Error("IndexedDB is not available in this environment");
    }
    this.name = name;
    this.storeName = store;
    this.version = version;
    this._db = null;
  }

  _open() {
    if (this._db) return Promise.resolve(this._db);
    return new Promise((resolve, reject) => {
      const req = indexedDB.open(this.name, this.version);
      req.onupgradeneeded = () => {
        const db = req.result;
        if (!db.objectStoreNames.contains(this.storeName)) db.createObjectStore(this.storeName);
      };
      req.onsuccess = () => {
        this._db = req.result;
        resolve(this._db);
      };
      req.onerror = () => reject(req.error || new Error("could not open IndexedDB"));
    });
  }

  async _tx(mode, fn) {
    const db = await this._open();
    return new Promise((resolve, reject) => {
      const tx = db.transaction(this.storeName, mode);
      const store = tx.objectStore(this.storeName);
      let result;
      try {
        result = fn(store);
      } catch (err) {
        reject(err);
        return;
      }
      tx.oncomplete = () => resolve(result);
      tx.onerror = () => reject(tx.error || new Error("IndexedDB transaction failed"));
      tx.onabort = () => reject(tx.error || new Error("IndexedDB transaction aborted"));
    });
  }

  async getItem(key) {
    const db = await this._open();
    const tx = db.transaction(this.storeName, "readonly");
    const req = tx.objectStore(this.storeName).get(key);
    const value = await requestToPromise(req);
    return value === undefined ? null : value;
  }

  async setItem(key, value) {
    await this._tx("readwrite", (store) => store.put(String(value), key));
  }

  async removeItem(key) {
    await this._tx("readwrite", (store) => store.delete(key));
  }

  close() {
    if (this._db) this._db.close();
    this._db = null;
  }
}

/** Convenience factory matching the common `new IndexedDBStorage(...)` usage. */
export function indexedDBStorage(name = "syncwell", store = "state") {
  return new IndexedDBStorage({ name, store });
}
