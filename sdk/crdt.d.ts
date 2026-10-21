// Type declarations for the client-side CRDT (mirror of internal/crdt).

export interface Timestamp {
  /** Lamport counter. */
  c: number;
  /** Authoring node id. */
  n: string;
}

export interface Register<V = unknown> {
  /** Value; absent on a tombstone. */
  v?: V;
  /** Logical timestamp. */
  t: Timestamp;
  /** Deleted marker. */
  d?: boolean;
}

/** Total order on timestamps: higher counter wins, ties break by node id. */
export function tsAfter(a: Timestamp, b: Timestamp): boolean;

/** RFC 8785-style deterministic JSON used for tie-breaking equal timestamps. */
export function stableJSON(value: unknown): string | undefined;

/** Per-replica Lamport clock. */
export class Clock {
  constructor(node: string);
  node: string;
  counter: number;
  tick(): Timestamp;
  /** Advance past a timestamp this replica has already observed. */
  observe(ts?: Timestamp | null): void;
}

/** State-based last-writer-wins map. */
export class LWWMap<T extends Record<string, unknown> = Record<string, unknown>> {
  readonly entries: Map<string, Register>;

  set<K extends keyof T>(key: K, value: T[K], ts: Timestamp): void;
  delete(key: keyof T, ts: Timestamp): void;
  mergeRegister(key: string, reg: Register): void;
  mergeState(state: Record<string, Register>): void;

  get<K extends keyof T>(key: K): T[K] | undefined;
  /** All registers, including tombstones. */
  snapshot(): Record<string, Register>;
  /** Visible key/value pairs only. */
  live(): Partial<T>;
}
