import { useId as reactUseId } from "react";

// Thin wrapper over React 18's useId so primitives that need to wire
// label/description/control associations (Input, Select, Modal, …) have
// a single, SSR-safe id source. Accepts an optional caller-supplied id
// so consumers can pin a stable id when they need to reference it.

export function useFieldId(provided?: string): string {
  const generated = reactUseId();
  return provided ?? generated;
}
