// Type declarations for the Node file persistence adapter.

import type { StorageLike } from "./syncwell.js";

export class FileStorage implements StorageLike {
  constructor(file: string);
  readonly file: string;
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
  clear(): void;
}

export function fileStorage(file: string): FileStorage;
