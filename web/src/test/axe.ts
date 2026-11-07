// Accessibility assertion helper built on axe-core (no matcher plugin needed).
//
// jsdom has no layout engine, so the rules that need rendering (colour
// contrast, scrollable regions) are switched off here. Colour contrast is
// covered by the design-token contrast tests instead.
import axe from "axe-core";
import { beforeAll } from "vitest";

export interface AxeViolation {
  id: string;
  impact: string | null | undefined;
  help: string;
  nodes: string[];
}

const OPTIONS: axe.RunOptions = {
  // only violations are read
  resultTypes: ["violations"],
  rules: {
    "color-contrast": { enabled: false },
    "scrollable-region-focusable": { enabled: false },
    region: { enabled: false }, // a page fragment is rendered without the shell landmarks
  },
};

// axe's first run in a worker is several times slower than the next ones: jsdom
// compiles its user-agent stylesheet's selectors and the engine warms up on the
// first getComputedStyle pass. Under load that first run alone outlasted a test's
// 5 s (#697), so each file that imports this helper pays it once, here, in a hook
// with its own budget, on a throwaway tree shaped like a page.
beforeAll(async () => {
  const warm = document.createElement("div");
  const row =
    '<li><a href="#r">Row</a> <button type="button" aria-pressed="false">Go</button>' +
    '<input type="checkbox" role="switch" aria-label="On"> <span aria-hidden="true">·</span></li>';
  warm.innerHTML = `<main><h1>Warm-up</h1><ul>${row.repeat(60)}</ul></main>`;
  document.body.append(warm);
  try {
    await axe.run(warm, OPTIONS);
  } finally {
    warm.remove();
  }
}, 60_000);

export async function axeViolations(root: Element): Promise<AxeViolation[]> {
  const result = await axe.run(root, OPTIONS);
  return result.violations.map((v) => ({
    id: v.id,
    impact: v.impact,
    help: v.help,
    nodes: v.nodes.map((n) => n.html.slice(0, 160)),
  }));
}
