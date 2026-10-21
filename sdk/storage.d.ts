// Type declarations for the browser persistence adapters.

import type { StorageLike } from "./syncwell.js";

export class MemoryStorage implements StorageLike {
  constructor(initial?: Record<string, string>);
  readonly items: Map<string, string>;
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  clear(): void;
}

export class LocalStorageAdapter implements StorageLike {
  constructor(storage?: Storage);
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export interface IndexedDBOptions {
  name?: string;
  store?: string;
  version?: number;
}

export class IndexedDBStorage implements StorageLike {
  constructor(opts?: IndexedDBOptions);
  getItem(key: string): Promise<string | null>;
  setItem(key: string, value: string): Promise<void>;
  removeItem(key: string): Promise<void>;
  close(): void;
}

export function indexedDBStorage(name?: string, store?: string): IndexedDBStorage;
