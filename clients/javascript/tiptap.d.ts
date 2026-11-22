import type { SequenceText } from "./text.js";
import type { Awareness } from "./awareness.js";

export interface ProseMirrorBinding {
  destroy(): void;
}

export declare function bindProseMirror(
  view: any,
  sequenceText: SequenceText,
  awareness?: Awareness | null
): ProseMirrorBinding;

export declare function syncwellCollaboration(options: {
  sequenceText: SequenceText;
  awareness?: Awareness;
}): any;
