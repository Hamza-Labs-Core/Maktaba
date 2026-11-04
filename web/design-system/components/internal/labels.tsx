import { createContext, useContext } from "react";
import type { ReactNode } from "react";

// The few words the design system's own controls say (an overlay's close button), in the
// page's language. The app gives them once at its root; with no provider they are English.

export interface Labels {
  close: string;
}

const LabelsContext = createContext<Labels>({ close: "Close" });

export function LabelsProvider({ labels, children }: { labels: Labels; children: ReactNode }) {
  return <LabelsContext.Provider value={labels}>{children}</LabelsContext.Provider>;
}

export function useLabels(): Labels {
  return useContext(LabelsContext);
}
