// Type declarations for the sequence CRDT foundation.

export interface SequenceNode {
  id: string;
  /** Id of the element this one was inserted after ("" = root). */
  after?: string;
  /** Fractional lexicographic index; enables flat, position-stable ordering. */
  rank?: string;
  text?: string;
  deleted?: boolean;
  /** Optional rich-text attributes carried with the element. */
  marks?: Record<string, unknown>;
}

/**
 * State-based sequence CRDT. Elements render in a deterministic order even
 * when replicas merge concurrently.
 */
export class SequenceDoc {
  readonly nodes: Map<string, SequenceNode>;

  apply(node: SequenceNode): void;
  insert(id: string, after: string, text: string): void;
  /** Insert an element with an explicit fractional rank. */
  insertRanked(id: string, rank: string, text: string, after?: string): void;
  delete(id: string): void;
  mark(id: string, marks: Record<string, unknown> | null): void;
  /** Merge another SequenceDoc or a plain snapshot object. */
  merge(other: SequenceDoc | Record<string, SequenceNode>): void;
  snapshot(): Record<string, SequenceNode>;
  /** Rendered visible text. */
  text(): string;
  /** Visible element ids in document order. */
  visibleIds(): string[];
}
