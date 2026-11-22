import type { SequenceText } from "./text.js";
import type { Awareness } from "./awareness.js";

export interface MonacoBinding {
  destroy(): void;
}

export declare function bindMonaco(
  editor: any,
  sequenceText: SequenceText,
  awareness?: Awareness | null
): MonacoBinding;
