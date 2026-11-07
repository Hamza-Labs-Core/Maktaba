// The flow drawn as a chart (story 54.41's picture of README §4.12), for the review pages and
// the listing page: an SVG made on the server from the checker's normalised graph, with no
// script. Core events on the left, then each plugin's stages and events in columns by their
// distance from the core; edges are arrows (a dashed one is a subscription to another
// plugin's event; a red one is on the cycle a refusal names). Every text is escaped.
import type { Graph, GraphNode } from "../../tools/plugin-sdk/js/src/flow";

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);

const W = 190;
const H = 44;
const GX = 60;
const GY = 18;

/** The chart of a graph; `cycle` is a refusal's path (display ids or node ids), drawn red. */
export function flowSVG(g: Graph, opts: { cycle?: string[]; title?: string; highlight?: string } = {}): string {
  if (!g.nodes.length) return "";
  const nodes = [...g.nodes].sort((a, b) => (a.id < b.id ? -1 : 1));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  // columns: the longest distance from a node with no incoming edge (bounded: a refused
  // graph can have a cycle)
  const col = new Map<string, number>(nodes.map((n) => [n.id, 0]));
  for (let pass = 0; pass < nodes.length; pass++) {
    let moved = false;
    for (const e of g.edges) {
      const c = (col.get(e.from) ?? 0) + 1;
      if (byId.has(e.to) && c > (col.get(e.to) ?? 0) && c <= nodes.length) {
        col.set(e.to, c);
        moved = true;
      }
    }
    if (!moved) break;
  }
  const columns: GraphNode[][] = [];
  for (const n of nodes) (columns[col.get(n.id) ?? 0] ??= []).push(n);
  const pos = new Map<string, { x: number; y: number }>();
  columns.forEach((list, c) => list.forEach((n, r) => pos.set(n.id, { x: 16 + c * (W + GX), y: 16 + r * (H + GY) })));
  const width = 32 + columns.length * (W + GX) - GX;
  const height = 32 + Math.max(...columns.map((l) => l?.length ?? 0)) * (H + GY) - GY;

  // the cycle's edges, from a path of display ids ("dev.x/one", "plugin:dev.x/a.done") or node ids
  const nodeOf = (p: string) =>
    byId.has(p) ? p : p.startsWith("plugin:") ? `event:${p.slice(7)}` : byId.has(`stage:${p}`) ? `stage:${p}` : `core:${p}`;
  const red = new Set<string>();
  const path = (opts.cycle ?? []).map(nodeOf);
  for (let i = 0; i + 1 < path.length; i++) red.add(`${path[i]}>${path[i + 1]}`);
  // a subscription edge goes event -> stage; an emit stage -> event: both are on the path

  const edges = g.edges
    .filter((e) => pos.has(e.from) && pos.has(e.to))
    .map((e) => {
      const a = pos.get(e.from)!;
      const b = pos.get(e.to)!;
      const x1 = a.x + W;
      const y1 = a.y + H / 2;
      const x2 = b.x;
      const y2 = b.y + H / 2;
      const back = x2 <= x1;
      const d = back
        ? `M${x1},${y1} C${x1 + 40},${y1 - 60} ${x2 - 40},${y2 - 60} ${x2},${y2}`
        : `M${x1},${y1} C${(x1 + x2) / 2},${y1} ${(x1 + x2) / 2},${y2} ${x2},${y2}`;
      const hot = red.has(`${e.from}>${e.to}`);
      return `<path d="${d}" fill="none" class="e ${esc(e.kind)}${hot ? " hot" : ""}" marker-end="url(#${hot ? "ah" : "a"})"><title>${esc(e.kind)}</title></path>`;
    })
    .join("");
  const boxes = nodes
    .map((n) => {
      const p = pos.get(n.id)!;
      const label = n.kind === "core" ? n.name : n.kind === "event" ? `⟶ ${n.name}` : n.name;
      const sub = n.kind === "core" ? "Maktaba" : n.plugin;
      const cls = `n ${n.kind}${opts.highlight && n.plugin === opts.highlight ? " mine" : ""}${path.includes(n.id) ? " hot" : ""}`;
      return `<g class="${esc(cls)}"><rect x="${p.x}" y="${p.y}" width="${W}" height="${H}" rx="${n.kind === "event" ? 18 : 6}"/><text x="${p.x + 10}" y="${p.y + 18}">${esc(label.slice(0, 28))}</text><text class="s" x="${p.x + 10}" y="${p.y + 34}">${esc(sub.slice(0, 32))}</text><title>${esc(n.id)}</title></g>`;
    })
    .join("");
  return `<svg xmlns="http://www.w3.org/2000/svg" class="flow" dir="ltr" viewBox="0 0 ${width} ${height}" width="${width}" height="${height}" role="img" aria-label="${esc(opts.title ?? "flow")}">
<style>.n rect{fill:var(--bg,#fff);stroke:var(--line,#999)}.n.core rect{stroke-dasharray:4 3}.n.event rect{fill:var(--line,#eee)}.n.mine rect{stroke:var(--accent,#1f6f5c);stroke-width:2}.n.hot rect{stroke:#c0392b;stroke-width:2}text{font:13px system-ui,sans-serif;fill:var(--fg,#111)}text.s{font-size:11px;fill:var(--muted,#666)}.e{stroke:var(--muted,#888);stroke-width:1.4}.e.subscribe{stroke-dasharray:5 4}.e.hot{stroke:#c0392b;stroke-width:2.2}</style>
<defs><marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0,0L10,5L0,10z" fill="#888"/></marker><marker id="ah" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0,0L10,5L0,10z" fill="#c0392b"/></marker></defs>
${edges}${boxes}</svg>`;
}
