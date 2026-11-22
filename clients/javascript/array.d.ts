import type { Syncwell } from "./syncwell.js";

export interface CRDTArrayOptions {
  prefix?: string;
}

export interface CRDTArrayEntry<T = any> {
  id: string;
  value: T;
}

export declare class CRDTArray<T = any> {
  readonly sw: Syncwell;
  readonly prefix: string;

  constructor(syncwell: Syncwell, opts?: CRDTArrayOptions);

  get length(): number;

  toArray(): T[];
  toEntries(): CRDTArrayEntry<T>[];
  get(index: number): T | undefined;
  getId(index: number): string | undefined;

  insert(index: number, value: T): string;
  push(...values: T[]): string | undefined;
  unshift(...values: T[]): string | undefined;

  delete(index: number): boolean;
  deleteById(id: string): boolean;
  move(fromIndex: number, toIndex: number): boolean;
  clear(): void;

  on(event: "change", cb: (items: T[]) => void): this;
  off(event: "change", cb: (items: T[]) => void): this;
  emit(event: string, ...args: any[]): void;
  destroy(): void;
}
