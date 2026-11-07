import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { validate } from "../../tools/plugin-sdk/js/src/manifest.js";
import { checkFlow } from "./flow.js";

const corpus = join(import.meta.dirname, "..", "..", "shared", "plugins", "testdata", "flows");

function upload(name: string) {
  const dir = join(corpus, name, "plugins");
  // git keeps no empty folder: a case of a flow alone has none
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .filter((f) => f.endsWith(".toml"))
    .sort()
    .map((f) => validate(readFileSync(join(dir, f))) as Record<string, unknown>);
}

describe("C12, the flow check (54.16 T09, T10)", () => {
  // T09: a three-plugin flow where A's event starts B, B's starts C and C's starts A,
  // submitted as one upload: refused as a whole with the path through all three
  it("refuses a three-plugin flow with a cycle, naming the path", () => {
    const r = checkFlow(readFileSync(join(corpus, "cycle_three_store", "flow.toml")), upload("cycle_three_store"), () => undefined);
    expect(r.ok).toBe(false);
    const cycle = r.findings.find((f) => f.code === "flow_cycle");
    expect(cycle?.path).toEqual([
      "dev.example.alpha/one",
      "plugin:dev.example.alpha/a.done",
      "dev.example.beta/two",
      "plugin:dev.example.beta/b.done",
      "dev.example.gamma/three",
      "plugin:dev.example.gamma/c.done",
      "dev.example.alpha/one",
    ]);
    expect(cycle?.en).toContain("dev.example.alpha/one → plugin:dev.example.alpha/a.done");
    expect(cycle?.ar).toContain("حلقة");
  });

  it("accepts a two-plugin chain uploaded together", () => {
    const r = checkFlow(readFileSync(join(corpus, "chain_two_store", "flow.toml")), upload("chain_two_store"), () => undefined);
    expect(r.findings).toEqual([]);
    expect(r.ok).toBe(true);
    expect(r.graph?.edges.some((e) => e.kind === "subscribe")).toBe(true);
  });

  it("accepts a member not uploaded when the store has it, and refuses it when not", () => {
    const flow = readFileSync(join(corpus, "chain_two_store", "flow.toml"));
    const [writer, silences] = upload("chain_two_store");
    const has = checkFlow(flow, [writer as Record<string, unknown>], (id) => (id === "dev.example.silences" ? silences : undefined));
    expect(has.ok).toBe(true);
    const lacks = checkFlow(flow, [writer as Record<string, unknown>], () => undefined);
    expect(lacks.ok).toBe(false);
    expect(lacks.findings.map((f) => f.code)).toContain("flow_missing_plugin");
  });

  // T10: the same answer as the Go checker on the corpus's store cases
  for (const name of readdirSync(corpus).filter((d) => {
    try {
      return JSON.parse(readFileSync(join(corpus, d, "case.json"), "utf8")).mode === "store";
    } catch {
      return false;
    }
  })) {
    it(`corpus ${name}`, () => {
      const want = JSON.parse(readFileSync(join(corpus, name, "expect.json"), "utf8")) as { findings: unknown[] };
      const flow = readFileSync(join(corpus, name, "flow.toml"), "utf8");
      // the corpus's flow members not uploaded are in the store; nothing else is
      const listed = new Set([...flow.matchAll(/^id = "([^"]+)"$/gm)].map((m) => m[1]));
      const r = checkFlow(flow, upload(name), (id) => (listed.has(id) ? { id } : undefined));
      expect(r.findings.map(({ en: _en, ar: _ar, ...f }) => f)).toEqual(want.findings);
    });
  }
});
