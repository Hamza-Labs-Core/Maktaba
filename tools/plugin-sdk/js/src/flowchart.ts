// The flow chart (Epic 54, README §4.12, story 54.41): one layout of a normalised flow
// graph (54.39 §3, flow.ts's Graph) drawn as SVG, used by the web's install screen and
// Plugins page, the store's review and listing pages, the SDK and maktaba.dev, so everyone
// sees the same picture. It depends on nothing (no TOML, no Node built-ins): the browser
// imports it as it is.
//
// Columns are the longest path from a source (core events first); rows are ordered by the
// barycentre of each node's predecessors, two passes, ties by id, so the same graph always
// draws the same. In Arabic the chart runs right to left. Every label is escaped text: no
// URL, image or markup from a plugin is ever drawn.

/** The shape of flow.ts's Graph, repeated here so the browser needs nothing else. */
export interface ChartNode {
  id: string;
  kind: "core" | "stage" | "event" | string;
  name: string;
  plugin: string;
  state: string;
  subject: string;
}

export interface ChartEdge {
  from: string;
  kind: string;
  to: string;
}

export interface ChartGraph {
  nodes: ChartNode[];
  edges: ChartEdge[];
}

export interface ChartOptions {
  /** the plugin drawn in the accent colour */
  highlight?: string;
  /** "ar" draws right to left */
  lang?: "en" | "ar";
  /** a core event's words ("audio.ready" -> "Audio is ready") */
  coreLabel?: (event: string) => string;
  /** a plugin's name in the reader's language */
  pluginName?: (id: string) => string;
  /** words for a plugin that is not installed or is off */
  missingWord?: string;
  offWord?: string;
  /** the chart's title and description, for screen readers */
  title?: string;
}

export interface PlacedNode extends ChartNode {
  layer: number;
  row: number;
  x: number;
  y: number;
}

export interface Layout {
  nodes: PlacedNode[];
  edges: ChartEdge[];
  width: number;
  height: number;
}

const COL = 230;
const ROW = 70;
const BOX_W = 190;
const BOX_H = 44;
const PAD = 16;

const cmp = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

/** Lays out a graph: each node's layer (column) and row. */
export function layout(g: ChartGraph, rtl = false): Layout {
  const ids = [...g.nodes].map((n) => n.id).sort(cmp);
  const byId = new Map(g.nodes.map((n) => [n.id, n]));
  const succ = new Map<string, string[]>();
  const pred = new Map<string, string[]>();
  for (const id of ids) {
    succ.set(id, []);
    pred.set(id, []);
  }
  for (const e of g.edges) {
    if (!byId.has(e.from) || !byId.has(e.to)) continue;
    (succ.get(e.from) as string[]).push(e.to);
    (pred.get(e.to) as string[]).push(e.from);
  }
  // longest path layering in Kahn's order (a cycle, which the check refuses, ends last)
  const indeg = new Map(ids.map((id) => [id, (pred.get(id) as string[]).length]));
  const layer = new Map<string, number>();
  let ready = ids.filter((id) => indeg.get(id) === 0);
  while (ready.length) {
    ready.sort(cmp);
    const v = ready.shift() as string;
    const l = Math.max(0, ...(pred.get(v) as string[]).map((p) => (layer.get(p) ?? -1) + 1));
    layer.set(v, l);
    for (const w of succ.get(v) as string[]) {
      indeg.set(w, (indeg.get(w) as number) - 1);
      if (indeg.get(w) === 0) ready.push(w);
    }
  }
  const deepest = Math.max(-1, ...layer.values());
  for (const id of ids) if (!layer.has(id)) layer.set(id, deepest + 1);
  const columns: string[][] = [];
  for (const id of ids) {
    const l = layer.get(id) as number;
    (columns[l] ??= []).push(id);
  }
  // rows: two barycentre passes over the predecessors' rows, ties by id
  const row = new Map<string, number>();
  for (const col of columns) col?.forEach((id, i) => row.set(id, i));
  for (let pass = 0; pass < 2; pass++) {
    for (let l = 1; l < columns.length; l++) {
      const col = columns[l];
      if (!col) continue;
      const centre = (id: string) => {
        const ps = (pred.get(id) as string[]).filter((p) => (layer.get(p) as number) < l);
        return ps.length ? ps.reduce((s, p) => s + (row.get(p) as number), 0) / ps.length : (row.get(id) as number);
      };
      const keyed = col.map((id) => ({ id, c: centre(id) }));
      keyed.sort((a, b) => a.c - b.c || cmp(a.id, b.id));
      columns[l] = keyed.map((k) => k.id);
      columns[l]?.forEach((id, i) => row.set(id, i));
    }
  }
  const tallest = Math.max(1, ...columns.map((c) => c?.length ?? 0));
  const width = PAD * 2 + Math.max(1, columns.length) * COL - (COL - BOX_W);
  const height = PAD * 2 + tallest * ROW - (ROW - BOX_H);
  const nodes: PlacedNode[] = [];
  columns.forEach((col, l) => {
    const offset = ((tallest - (col?.length ?? 0)) * ROW) / 2;
    col?.forEach((id, r) => {
      const n = byId.get(id) as ChartNode;
      const left = PAD + l * COL;
      nodes.push({
        ...n,
        layer: l,
        row: r,
        x: rtl ? width - left - BOX_W : left,
        y: PAD + offset + r * ROW,
      });
    });
  });
  nodes.sort((a, b) => cmp(a.id, b.id));
  // one line per pair: a request and an "after" between the same two stages draw once
  const seen = new Set<string>();
  const edges = g.edges.filter((e) => {
    const k = `${e.from}\u0000${e.to}`;
    if (!byId.has(e.from) || !byId.has(e.to) || seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  return { nodes, edges, width, height };
}

/** Text for SVG: &, <, >, quotes escaped; nothing is ever markup. */
export function esc(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

function clip(s: string, n = 26): string {
  const chars = [...s];
  return chars.length > n ? `${chars.slice(0, n - 1).join("")}…` : s;
}

/** The words of a node as the chart shows them. */
export function nodeLabel(n: ChartNode, o: ChartOptions = {}): { main: string; sub: string } {
  if (n.kind === "core") return { main: o.coreLabel?.(n.name) ?? n.name, sub: o.coreLabel ? n.name : "" };
  const plugin = o.pluginName?.(n.plugin) ?? n.plugin;
  const state = n.state === "missing" ? o.missingWord ?? "not installed" : n.state === "installed" ? o.offWord ?? "off" : "";
  // the state first, so a long plugin id never clips it away
  return { main: n.name, sub: state ? `${state} · ${plugin}` : plugin };
}

/** The steps of the chart in reading order: the text beside it for screen readers. */
export function flowSteps(g: ChartGraph, o: ChartOptions = {}): string[] {
  const l = layout(g);
  const byId = new Map(l.nodes.map((n) => [n.id, n]));
  const label = (id: string) => {
    const n = byId.get(id) as PlacedNode;
    const w = nodeLabel(n, o);
    return n.kind === "core" ? w.main : `${w.main} (${w.sub})`;
  };
  return [...l.edges]
    .sort((a, b) => {
      const x = byId.get(a.from) as PlacedNode;
      const y = byId.get(b.from) as PlacedNode;
      return x.layer - y.layer || x.row - y.row || cmp(a.to, b.to);
    })
    .map((e) => `${label(e.from)} → ${label(e.to)}`);
}

/** One drawn edge: its SVG path and whether it is dashed (a member off or missing). */
export interface DrawnEdge {
  key: string;
  kind: string;
  d: string;
  off: boolean;
}

/** One drawn node: its box, its two lines of text and how it is marked. */
export interface DrawnNode {
  id: string;
  kind: string;
  x: number;
  y: number;
  w: number;
  h: number;
  rx: number;
  main: string;
  sub: string;
  tx: number;
  anchor: "start" | "end";
  highlight: boolean;
  off: boolean;
}

/** The chart as geometry and plain strings: what renderSvg writes and what an app draws
 * with its own elements (the web draws it as React SVG, never as markup it was sent). */
export interface ChartModel {
  width: number;
  height: number;
  rtl: boolean;
  title: string;
  description: string;
  edges: DrawnEdge[];
  nodes: DrawnNode[];
}

export function chartModel(g: ChartGraph, o: ChartOptions = {}): ChartModel {
  const rtl = o.lang === "ar";
  const l = layout(g, rtl);
  const byId = new Map(l.nodes.map((n) => [n.id, n]));
  const edges = [...l.edges]
    .sort((a, b) => cmp(a.from, b.from) || cmp(a.to, b.to))
    .map((e) => {
      const a = byId.get(e.from) as PlacedNode;
      const b = byId.get(e.to) as PlacedNode;
      const x1 = rtl ? a.x : a.x + BOX_W;
      const x2 = rtl ? b.x + BOX_W : b.x;
      const y1 = a.y + BOX_H / 2;
      const y2 = b.y + BOX_H / 2;
      const mid = (x1 + x2) / 2;
      return {
        key: `${e.from}>${e.to}`,
        kind: e.kind,
        d: `M${x1} ${y1} C${mid} ${y1} ${mid} ${y2} ${x2} ${y2}`,
        off: a.state !== "enabled" || b.state !== "enabled",
      };
    });
  const nodes = l.nodes.map((n) => {
    const w = nodeLabel(n, o);
    return {
      id: n.id,
      kind: n.kind,
      x: n.x,
      y: n.y,
      w: BOX_W,
      h: BOX_H,
      rx: n.kind === "event" ? BOX_H / 2 : n.kind === "core" ? 12 : 4,
      main: clip((n.kind === "event" ? "◆ " : "") + w.main),
      sub: clip(w.sub, 32),
      tx: rtl ? n.x + BOX_W - 10 : n.x + 10,
      anchor: rtl ? ("end" as const) : ("start" as const),
      highlight: !!o.highlight && n.plugin === o.highlight,
      off: n.state !== "enabled",
    };
  });
  return {
    width: l.width,
    height: l.height,
    rtl,
    title: o.title ?? "Plugin flow",
    description: flowSteps(g, o).join("; "),
    edges,
    nodes,
  };
}

/** The style the chart's classes take; colours from --fc-* variables, with defaults. */
export const CHART_STYLE =
  ".flowchart{font:12px system-ui,sans-serif}.fc-edge{fill:none;stroke:var(--fc-line,#8a8f98);stroke-width:1.5}.fc-edge.fc-off{stroke-dasharray:5 4}.fc-box{fill:var(--fc-bg,#fff);stroke:var(--fc-line,#8a8f98);stroke-width:1.2}.fc-core .fc-box{fill:var(--fc-core,#eef1f5)}.fc-hl .fc-box{stroke:var(--fc-accent,#2f6fde);stroke-width:2.5}.fc-off .fc-box{stroke-dasharray:5 4;opacity:.7}.fc-main{fill:var(--fc-text,#1d2129);font-weight:600}.fc-sub{fill:var(--fc-muted,#5b616b);font-size:10.5px}.fc-arrow{fill:var(--fc-line,#8a8f98)}";

/** The class list of a drawn node. */
export function nodeClass(n: DrawnNode): string {
  return ["fc-node", `fc-${n.kind}`, ...(n.highlight ? ["fc-hl"] : []), ...(n.off ? ["fc-off"] : [])].join(" ");
}

/** The chart as an SVG document (a string, every text escaped), for the store's pages,
 * maktaba.dev and files. */
export function renderSvg(g: ChartGraph, o: ChartOptions = {}): string {
  const m = chartModel(g, o);
  const parts: string[] = [
    `<svg xmlns="http://www.w3.org/2000/svg" class="flowchart" viewBox="0 0 ${m.width} ${m.height}" width="${m.width}" height="${m.height}" role="img" aria-labelledby="fc-t fc-d" direction="${m.rtl ? "rtl" : "ltr"}">`,
    `<title id="fc-t">${esc(m.title)}</title>`,
    `<desc id="fc-d">${esc(m.description)}</desc>`,
    `<style>${CHART_STYLE}</style>`,
    `<defs><marker id="fc-a" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path class="fc-arrow" d="M0 0 L8 4 L0 8 z"/></marker></defs>`,
  ];
  for (const e of m.edges) {
    parts.push(`<path class="fc-edge fc-${esc(e.kind)}${e.off ? " fc-off" : ""}" d="${e.d}" marker-end="url(#fc-a)"/>`);
  }
  for (const n of m.nodes) {
    parts.push(
      `<g class="${esc(nodeClass(n))}" data-id="${esc(n.id)}"><rect class="fc-box" x="${n.x}" y="${n.y}" width="${n.w}" height="${n.h}" rx="${n.rx}"/>` +
        `<text class="fc-main" x="${n.tx}" y="${n.y + 18}" text-anchor="${n.anchor}">${esc(n.main)}</text>` +
        `<text class="fc-sub" x="${n.tx}" y="${n.y + 34}" text-anchor="${n.anchor}">${esc(n.sub)}</text></g>`,
    );
  }
  parts.push("</svg>");
  return parts.join("");
}

/** The chart as text, for the terminal (mkp flow) and plain docs: one line per edge in
 * reading order. */
export function renderText(g: ChartGraph, o: ChartOptions = {}): string {
  return flowSteps(g, o).join("\n");
}

/** The chart as Mermaid, for a README. */
export function renderMermaid(g: ChartGraph, o: ChartOptions = {}): string {
  const l = layout(g);
  const ref = new Map(l.nodes.map((n, i) => [n.id, `n${i}`]));
  const quote = (s: string) => `"${s.replace(/"/g, "'")}"`;
  const lines = [o.lang === "ar" ? "graph RL" : "graph LR"];
  for (const n of l.nodes) {
    const w = nodeLabel(n, o);
    const text = quote(n.kind === "core" ? w.main : `${w.main}<br/>${w.sub}`);
    const r = ref.get(n.id) as string;
    lines.push(n.kind === "core" ? `  ${r}([${text}])` : n.kind === "event" ? `  ${r}{{${text}}}` : `  ${r}[${text}]`);
  }
  for (const e of l.edges) {
    const off = (l.nodes.find((n) => n.id === e.from)?.state ?? "enabled") !== "enabled";
    lines.push(`  ${ref.get(e.from)} ${off ? "-.->" : "-->"} ${ref.get(e.to)}`);
  }
  return lines.join("\n");
}
