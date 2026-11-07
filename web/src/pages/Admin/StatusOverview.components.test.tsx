// Epic 51 story 51.14: System health's Components line names the component by its title
// and its reason in short words, in both languages.
import { describe, expect, it } from "vitest";
import { itemName, itemReason } from "./StatusOverview";
import { lookupMessage, type Locale } from "../../lib/i18n";
import type { Fmt } from "../../components/queue/format";
import type { StatusItem } from "../../lib/systemStatus";
import { firstPartyStages } from "../../test/firstPartyStages";

function tFor(locale: Locale) {
  return (key: string, vars?: Record<string, string | number>) => {
    const hit = lookupMessage(locale, key);
    if (hit === undefined) return key;
    return hit.replace(/\{(\w+)\}/g, (m, k) => (vars && k in vars ? String(vars[k]) : m));
  };
}

const failed: StatusItem = {
  id: "components",
  group: "services",
  kind: "components",
  name: "Components",
  status: "degraded",
  code: "components_failed",
  params: { failed: 1, component: "llm", why: "crash_loop", machine: "home" },
  reason: "1 failed: llm (crash loop).",
  attention: true,
  last_checked: new Date().toISOString(),
  help: "components",
};

const fmt = { dur: (s: number) => `${s}s` } as unknown as Fmt;
const n = (v: number) => String(v);

describe("the Components line", () => {
  it("reads in English", () => {
    const t = tFor("en");
    expect(itemReason(failed, t, fmt, n)).toBe(
      `1 failed: ${t("components.llm.title")} (crash loop).`
    );
    expect(itemReason({ ...failed, code: "components_ok", params: undefined }, t, fmt, n)).toBe(
      "All parts running."
    );
  });

  it("reads in Arabic", () => {
    const t = tFor("ar");
    const s = itemReason(failed, t, fmt, n);
    expect(s).toContain(t("components.llm.title"));
    expect(s).toContain("تعطّل متكرر");
    expect(s).not.toContain("crash");
  });

  it("names the line itself in the reader's language", () => {
    expect(itemName(failed, tFor("en"))).toBe("Components");
    expect(itemName(failed, tFor("ar"))).toBe(tFor("ar")("components.page.title"));
  });

  it("keeps an unknown reason's code readable", () => {
    const t = tFor("en");
    expect(
      itemReason({ ...failed, params: { ...failed.params, why: "from_the_future" } }, t, fmt, n)
    ).toContain("(from the future)");
  });
});

// Files outside src/ are read at run time (the web image builds from web/ alone).
const FS = "node:fs";
const { readFileSync, readdirSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
  readdirSync: (path: string) => string[];
};
const ARABIC = /[؀-ۿ]/;
const LATIN = /[A-Za-z]/;

describe("every System health item has its name in Arabic", () => {
  it('each line the API names by a fixed id (base("id", ...))', () => {
    const dir = "../api/internal/handlers/systemstatus";
    const ids = new Set<string>();
    for (const f of readdirSync(dir)) {
      if (!f.endsWith(".go") || f.endsWith("_test.go")) continue;
      for (const m of readFileSync(`${dir}/${f}`, "utf8").matchAll(/base\("([a-z_]+)",/g))
        ids.add(m[1]!);
    }
    expect(ids).toContain("components");
    const ar = tFor("ar");
    for (const id of ids) {
      const it: StatusItem = { ...failed, id, kind: "x", name: id };
      // a brand (Plex) may stay in Latin letters; the id never shows
      expect(itemName(it, ar), id).toMatch(ARABIC);
      expect(itemName(it, ar), id).not.toContain(id);
    }
  });

  it("each model a stage names (dependency:model:<rules section>), never its id", () => {
    const graph = readFileSync("../pipeline/src/maktaba_pipeline/engine/graph.py", "utf8");
    const sections = new Set([...graph.matchAll(/model_rules="([a-z_]+)"/g)].map((m) => m[1]!));
    // a stage a first-party plugin declares keeps its model_rules in its manifest
    for (const st of firstPartyStages().values())
      if (typeof st.model_rules === "string") sections.add(st.model_rules);
    expect(sections).toContain("tagcheck");
    // a stage with no model_rules parks on the default section
    sections.add("model");
    const en = tFor("en");
    const ar = tFor("ar");
    for (const s of sections) {
      const it: StatusItem = {
        ...failed,
        id: `dependency:model:${s}`,
        kind: "dependency",
        name: `dependency:model:${s}`,
      };
      expect(itemName(it, en), s).not.toContain(s === "model" ? "(" : s);
      expect(itemName(it, ar), s).toMatch(ARABIC);
      expect(itemName(it, ar), s).not.toMatch(LATIN);
    }
    const tag: StatusItem = {
      ...failed,
      id: "dependency:model:tagcheck",
      kind: "dependency",
      name: "dependency:model:tagcheck",
    };
    expect(itemName(tag, en)).toBe("Language model (tag check)");
    expect(itemName({ ...tag, name: "dependency:model:model" }, en)).toBe("Language model");
    // a section with no words yet keeps its id
    expect(itemName({ ...tag, name: "dependency:model:future" }, en)).toBe(
      "Language model (future)"
    );
  });

  it("each of a role's processes by its own name (two pipeline tiles never read the same)", () => {
    const proc = (name: string, details: Record<string, unknown>): StatusItem => ({
      ...failed,
      id: `process:${name}`,
      kind: "process",
      name,
      details,
    });
    const main = proc("pipeline", { role: "pipeline" });
    const index = proc("pipeline index", { role: "pipeline", instance: "index" });
    const en = tFor("en");
    const ar = tFor("ar");
    expect(itemName(main, en)).toBe("Processing pipeline");
    expect(itemName(index, en)).toBe("Processing pipeline · search index");
    expect(itemName(index, ar)).not.toBe(itemName(main, ar));
    expect(itemName(index, ar)).toMatch(ARABIC);
    expect(itemName(index, ar)).not.toMatch(LATIN);
  });
});
