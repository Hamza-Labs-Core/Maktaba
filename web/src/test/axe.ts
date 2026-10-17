// Accessibility assertion helper built on axe-core (no matcher plugin needed).
//
// jsdom has no layout engine, so the rules that need rendering (colour
// contrast, scrollable regions) are switched off here. Colour contrast is
// covered by the design-token contrast tests instead.
import axe from "axe-core";

export interface AxeViolation {
  id: string;
  impact: string | null | undefined;
  help: string;
  nodes: string[];
}

export async function axeViolations(root: Element): Promise<AxeViolation[]> {
  const result = await axe.run(root, {
    rules: {
      "color-contrast": { enabled: false },
      "scrollable-region-focusable": { enabled: false },
      region: { enabled: false }, // a page fragment is rendered without the shell landmarks
    },
  });
  return result.violations.map((v) => ({
    id: v.id,
    impact: v.impact,
    help: v.help,
    nodes: v.nodes.map((n) => n.html.slice(0, 160)),
  }));
}
