// Story 54.41 T01, T02 (ts): the chart's layout is deterministic and follows the flow
// (each edge goes to a later column), Arabic mirrors it, every label is escaped text, and
// three corpus graphs draw byte for byte as their committed SVGs.
import { strict as assert } from "node:assert";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { flowSteps, layout, renderMermaid, renderSvg, renderText, type ChartGraph } from "../src/flowchart.js";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..", "..", "..", "..");
const corpus = join(root, "shared", "plugins", "testdata", "flows");
const charts = join(here, "..", "..", "test", "charts");
const graphOf = (name: string) => (JSON.parse(readFileSync(join(corpus, name, "expect.json"), "utf8")) as { graph: ChartGraph }).graph;

for (const name of ["single", "chain_two_home", "cycle_three_home_one_off", "sixteen_deep"]) {
  test(`chart/${name}`, () => {
    const g = graphOf(name);
    const l = layout(g);
    const at = new Map(l.nodes.map((n) => [n.id, n]));
    for (const e of g.edges) assert.ok(at.get(e.to)!.layer > at.get(e.from)!.layer, `${e.from} → ${e.to}`);
    const svg = renderSvg(g, { highlight: "dev.example.chapter-writer" });
    assert.equal(svg, renderSvg(g, { highlight: "dev.example.chapter-writer" }));
    const file = join(charts, `${name}.svg`);
    if (process.env.UPDATE_GOLDEN === "1" || !existsSync(file)) writeFileSync(file, svg + "\n");
    assert.equal(svg + "\n", readFileSync(file, "utf8"));
  });
}

test("a chain reads in order, as text and as Mermaid", () => {
  const g = graphOf("chain_two_home");
  assert.deepEqual(flowSteps(g, { coreLabel: (e) => (e === "audio.ready" ? "Audio is ready" : e) }), [
    "Audio is ready → find_silences (dev.example.silences)",
    "find_silences (dev.example.silences) → silences.found (dev.example.silences)",
    "silences.found (dev.example.silences) → write_chapters (dev.example.chapter-writer)",
    "write_chapters (dev.example.chapter-writer) → tidy (dev.example.chapter-writer)",
  ]);
  assert.equal(renderText(g).split("\n").length, 4);
  assert.ok(renderMermaid(g).startsWith("graph LR\n"));
  assert.ok(renderMermaid(g, { lang: "ar" }).startsWith("graph RL\n"));
});

test("Arabic runs right to left", () => {
  const g = graphOf("chain_two_home");
  const ltr = new Map(layout(g).nodes.map((n) => [n.id, n.x]));
  const rtl = new Map(layout(g, true).nodes.map((n) => [n.id, n.x]));
  const core = "core:audio.ready";
  const last = "stage:dev.example.chapter-writer/tidy";
  assert.ok(ltr.get(core)! < ltr.get(last)!);
  assert.ok(rtl.get(core)! > rtl.get(last)!);
  assert.ok(renderSvg(g, { lang: "ar" }).includes('direction="rtl"'));
});

test("T02: labels are text, never markup or a link", () => {
  const g: ChartGraph = {
    nodes: [
      { id: "core:audio.ready", kind: "core", name: "audio.ready", plugin: "", state: "enabled", subject: "video" },
      { id: "stage:dev.example.x/s", kind: "stage", name: "s", plugin: "dev.example.x", state: "enabled", subject: "video" },
    ],
    edges: [{ from: "core:audio.ready", kind: "trigger", to: "stage:dev.example.x/s" }],
  };
  const svg = renderSvg(g, { pluginName: () => `<script>alert(1)</script><a href="https://example.com">x</a>`, title: "<b>t</b>" });
  assert.ok(!svg.includes("<script") && !svg.includes("<a ") && !svg.includes('href="') && !svg.includes("<image"));
  assert.ok(svg.includes("&lt;script&gt;") && svg.includes("&lt;b&gt;t&lt;/b&gt;"));
});
