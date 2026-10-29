// Type declarations for the Syncwell client. The runtime is plain ESM/CJS with
// no dependencies; these types are hand-maintained alongside syncwell.js.

import type { Clock, LWWMap } from "./crdt.js";

/** A document whose keys map to arbitrary JSON-ish values. */
export type DocumentState = Record<string, unknown>;

export interface StorageLike {
  getItem(key: string): string | null | Promise<string | null>;
  setItem(key: string, value: string): void | Promise<void>;
  removeItem?(key: string): void | Promise<void>;
}

/** Minimal structural WebSocket (browser WebSocket or the `ws` package). */
export interface WebSocketLike {
  readyState: number;
  onopen: ((event: unknown) => void) | null;
  onclose: ((event: unknown) => void) | null;
  onmessage: ((event: { data: string }) => void) | null;
  onerror: ((event: unknown) => void) | null;
  send(data: string): void;
  close(): void;
}

export interface WebSocketConstructor {
  new (url: string, protocols?: string | string[]): WebSocketLike;
}

export interface SyncwellOptions {
  /** Room token when the server runs with `-secret`. */
  token?: string;
  /** Token carrier (default: `subprotocol`; use `query` only for legacy servers). */
  tokenTransport?: "query" | "subprotocol";
  /** Resolve `/route?room=` before each connection/reconnect (default false). */
  followOwner?: boolean;
  /** Override fetch used by followOwner (defaults to global fetch). */
  fetch?: (url: string, init?: { headers?: Record<string, string> }) => Promise<{
    ok: boolean;
    status: number;
    json(): Promise<unknown>;
  }>;
  /** Stable id used as the CRDT node id; keep it stable with `persist`. */
  clientId?: string;
  /** Persist the local replica + Lamport clock in `storage`. */
  persist?: boolean;
  /** Storage-compatible object (defaults to `localStorage` when `persist`). */
  storage?: StorageLike | null;
  /** Override the localStorage key. */
  storageKey?: string;
  /** Reconnect backoff ceiling in ms (default 8000). */
  maxReconnectDelay?: number;
  /** Inject a WebSocket constructor (required in Node). */
  WebSocket?: WebSocketConstructor | null;
  /** Use state-vector delta sync on reconnect (default true). */
  delta?: boolean;
  /** Maximum undo groups retained in memory (default 100). */
  historyLimit?: number;
}

export interface HistoryEvent {
  canUndo: boolean;
  canRedo: boolean;
}

export interface PresenceEvent {
  type: "presence";
  protocol?: number;
  epoch?: number;
  clientId?: string;
  data?: unknown;
}

export interface LeaveEvent {
  type: "leave";
  protocol?: number;
  epoch?: number;
  clientId?: string;
}

export interface SyncwellEvents<State extends DocumentState> {
  open: () => void;
  close: () => void;
  change: (live: State) => void;
  presence: (event: PresenceEvent) => void;
  leave: (event: LeaveEvent) => void;
  error: (error: SyncwellError) => void;
  history: (event: HistoryEvent) => void;
}

export type SyncwellErrorCode =
  | "NO_WEBSOCKET"
  | "BAD_FRAME"
  | "SERVER_ERROR"
  | "STALE_EPOCH"
  | "UNSUPPORTED_PROTOCOL"
  | "STORAGE_READ"
  | "STORAGE_WRITE"
  | (string & {});

export class SyncwellError extends Error {
  constructor(code: SyncwellErrorCode, message: string, cause?: unknown);
  code: SyncwellErrorCode;
  cause?: unknown;
}

/** Protocol version carried on every frame. */
export const protocolVersion: number;

/**
 * A live, offline-capable replica of one room.
 *
 * @typeParam State the document shape, e.g. `Syncwell<{ title: string; count: number }>`
 */
export class Syncwell<State extends DocumentState = DocumentState> {
  constructor(baseURL: string, room?: string, opts?: SyncwellOptions);

  readonly url: string;
  readonly clientId: string;
  epoch: number;
  readonly clock: Clock;
  readonly doc: LWWMap<State>;
  /** Resolves after the initial persistence load (IndexedDB/localStorage/file). */
  readonly ready: Promise<void>;

  /** Subscribe to an event. Returns `this` for chaining. */
  on<K extends keyof SyncwellEvents<State>>(event: K, cb: SyncwellEvents<State>[K]): this;
  /** Remove one handler (or all handlers for the event when `cb` is omitted). */
  off<K extends keyof SyncwellEvents<State>>(event: K, cb?: SyncwellEvents<State>[K]): this;

  /** Open the socket (idempotent; auto-reconnects after a drop). */
  connect(): this;
  /** Close the socket and stop reconnecting. */
  close(): void;

  /** Optimistically write a key, then sync it to the server. */
  set<K extends keyof State>(key: K, value: State[K]): void;
  delete(key: keyof State): void;

  /** Group writes into one change event and one undo step. */
  batch<T>(fn: () => T): T;

  /** Undo/redo local edits. Remote edits are never silently overwritten. */
  undo(): boolean;
  redo(): boolean;
  canUndo(): boolean;
  canRedo(): boolean;
  clearHistory(): void;

  /** Ephemeral, non-persisted broadcast (cursors, selections, presence). */
  presence(data: unknown): void;

  /** Read a key from the local replica. */
  get<K extends keyof State>(key: K): State[K] | undefined;
  /** The visible (non-deleted) document. */
  live(): State;
}
