import type { Syncwell, SyncwellOptions } from "./syncwell.js";
import type { Awareness, AwarenessOptions } from "./awareness.js";
import type { CRDTArray } from "./array.js";

export interface SyncwellProviderProps {
  client?: Syncwell;
  url?: string;
  room?: string;
  options?: SyncwellOptions & { awareness?: AwarenessOptions };
  children?: any;
}

export declare function SyncwellProvider(props: SyncwellProviderProps): any;

export declare function useSyncwell(): Syncwell;

export declare function useAwareness(): Awareness;

export declare function useStorage<T = any>(key: string, fallback?: T): T;

export declare function usePresence<T extends Record<string, any> = Record<string, any>>(
  initialState?: T
): [T, (partial: Partial<T>) => void];

export declare function useArray<T = any>(prefix?: string): [T[], CRDTArray<T>];

export declare function useOthers(): Array<Record<string, any>>;

export declare function useSelf(): Record<string, any>;

export interface MutationContext {
  set(key: string, value: any): void;
  delete(key: string): void;
  get(key: string): any;
  batch<R>(fn: () => R): R;
}

export declare function useMutation<Args extends any[], R>(
  fn: (ctx: MutationContext, ...args: Args) => R
): (...args: Args) => R;

export interface HistoryState {
  undo(): void;
  redo(): void;
  canUndo: boolean;
  canRedo: boolean;
}

export declare function useHistory(): HistoryState;

export interface LiveCursorsProps {
  containerRef?: { current: HTMLElement | null };
  propagate?: boolean;
  renderCursor?: (props: { peer: Record<string, any>; color: string; name: string; key: string }) => any;
  className?: string;
  style?: Record<string, any>;
}

export declare function LiveCursors(props?: LiveCursorsProps): any;

export interface LiveAvatarsProps {
  limit?: number;
  size?: number;
  includeSelf?: boolean;
  className?: string;
  style?: Record<string, any>;
  renderAvatar?: (props: {
    user: Record<string, any>;
    color: string;
    name: string;
    initials: string;
    index: number;
  }) => any;
}

export declare function LiveAvatars(props?: LiveAvatarsProps): any;
