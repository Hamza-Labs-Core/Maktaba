// The flow drawn as a chart on the store's review and listing pages: the plugin SDK's one
// layout (tools/plugin-sdk/js/src/flowchart.ts, story 54.41), the same picture the home's
// Plugins page, its install screen and maktaba.dev draw. An SVG string, every text escaped,
// no script. A refused flow's cycle is named in C12's words beside it.
import { renderSvg } from "../../tools/plugin-sdk/js/src/flowchart";
import type { Graph } from "../../tools/plugin-sdk/js/src/flow";

/** The chart of a flow's graph; `highlight` outlines one plugin's stages. */
export function flowSVG(g: Graph, opts: { title?: string; highlight?: string; lang?: "en" | "ar" } = {}): string {
  if (!g.nodes.length) return "";
  return renderSvg(g, { ...opts, title: opts.title ?? "flow" });
}
