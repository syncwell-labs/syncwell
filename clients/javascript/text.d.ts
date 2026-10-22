// Type declarations for the collaborative sequence text wrapper.

import type { Syncwell } from "./syncwell.js";
import type { SequenceNode } from "./sequence.js";

export interface TextNode extends SequenceNode {
  rank?: string;
  marks?: Record<string, unknown>;
}

export interface SequenceTextOptions {
  /** Register key prefix in the room (default "text:"). */
  prefix?: string;
  clientId?: string;
}

export function rankBetween(left: string, right: string | null): string;

/**
 * Character-level collaborative text stored as sequence nodes in a Syncwell
 * room. All edits are local-first, offline-capable, undoable, and persisted by
 * the wrapped client.
 */
export class SequenceText {
  constructor(syncwell: Syncwell<any>, opts?: SequenceTextOptions);

  readonly prefix: string;
  readonly clientId: string;

  on(event: "change", cb: (text: string) => void): this;

  insertAt(index: number, value: string): string[];
  deleteAt(index: number, count?: number): number;
  replaceRange(index: number, count: number, value: string): void;
  markRange(index: number, count: number, marks: Record<string, unknown> | null): void;

  text(): string;
  visibleIds(): string[];
  marksAt(index: number): Record<string, unknown> | undefined;
  readonly length: number;

  close(): void;
}
