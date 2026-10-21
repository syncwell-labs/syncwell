// Type declarations for the ephemeral awareness API.

import type { Syncwell } from "./syncwell.js";

export interface AwarenessUser {
  id: string;
  name?: string;
  [key: string]: unknown;
}

export interface AwarenessCursor {
  /** Caret index in the visible sequence. */
  index: number;
  /** End of the selected range (equal to index for a caret). */
  selectionEnd: number;
}

export interface AwarenessState {
  user: AwarenessUser;
  color: string;
  cursor?: AwarenessCursor | null;
  selection?: unknown;
  status?: string;
  [key: string]: unknown;
}

export interface AwarenessPeer extends AwarenessState {
  clientId: string;
  self: boolean;
  lastSeen: number;
}

export interface AwarenessOptions {
  clientId?: string;
  user?: AwarenessUser;
  name?: string;
  color?: string;
  state?: Partial<AwarenessState>;
  heartbeat?: number;
  timeout?: number;
  start?: boolean;
}

export interface AwarenessEvents {
  update: (states: AwarenessPeer[]) => void;
  join: (peer: AwarenessPeer) => void;
  change: (peer: AwarenessPeer) => void;
  remove: (clientId: string) => void;
  error: (error: unknown) => void;
}

export class Awareness {
  constructor(syncwell: Syncwell<any>, opts?: AwarenessOptions);

  readonly clientId: string;
  user: AwarenessUser;
  color: string;

  on<K extends keyof AwarenessEvents>(event: K, cb: AwarenessEvents[K]): this;
  off<K extends keyof AwarenessEvents>(event: K, cb?: AwarenessEvents[K]): this;

  start(): this;
  stop(): this;
  close(): void;

  setUser(user: AwarenessUser): this;
  setColor(color: string): this;
  setCursor(cursor: { index: number; selectionEnd?: number } | null): this;
  setSelection(index: number, selectionEnd?: number): this;
  update(partial: Partial<AwarenessState>): this;

  get(clientId: string): AwarenessPeer | undefined;
  getStates(): AwarenessPeer[];
}
