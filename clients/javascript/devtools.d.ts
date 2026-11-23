import type { Syncwell } from "./syncwell.js";
import type { Awareness } from "./awareness.js";

export interface DevToolsOptions {
  awareness?: Awareness;
  container?: HTMLElement;
  position?: "bottom-right" | "bottom-left" | "top-right" | "top-left";
}

export interface DevToolsController {
  open(): void;
  close(): void;
  unmount(): void;
}

export declare function mountDevTools(
  syncwell: Syncwell<any>,
  options?: DevToolsOptions
): DevToolsController;

export declare function escapeHtml(str: unknown): string;
export declare function sanitizeColor(color: unknown): string;
