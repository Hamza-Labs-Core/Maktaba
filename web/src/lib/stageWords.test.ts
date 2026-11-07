// Every stage the pipeline graph ships (engine/graph.py GRAPH, the legacy stages too, and the
// stages Maktaba's own first-party plugins declare in their manifests) has
// its title in English (the graph's own label) and in Arabic, so a new stage without words
// fails here; a stage with no words keeps the label the API sent.
import { describe, expect, it } from "vitest";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { lookupMessage, type Locale } from "./i18n";
import { stageTitle, titledSnapshot } from "./stageWords";
import type { QueuesSnapshot, StageInfo } from "./queueApi";
import { firstPartyStages, graphFirstPartyNames } from "../test/firstPartyStages";

// the graph is outside src/: read it at run time, as help.coverage.test.ts does
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};

/** name -> label of each stage in the graph: StageDef(name=..., label=...), _legacy(...), and
 * each _fp("<name>") (a stage a first-party plugin's manifest defines, with its label there). */
function graphStages(): { stages: Map<string, string>; missing: string[] } {
  const src = readFileSync("../pipeline/src/maktaba_pipeline/engine/graph.py", "utf8");
  const out = new Map<string, string>();
  for (const m of src.matchAll(/\bname="([a-z0-9_]+)",\s*label="([^"]+)"/g)) out.set(m[1]!, m[2]!);
  for (const m of src.matchAll(/_legacy\(\s*"([a-z0-9_]+)",\s*"([^"]+)"/g)) out.set(m[1]!, m[2]!);
  const declared = firstPartyStages();
  const missing: string[] = [];
  for (const name of graphFirstPartyNames(src)) {
    const label = declared.get(name)?.label;
    if (typeof label !== "string") missing.push(name);
    else out.set(name, label);
  }
  return { stages: out, missing };
}

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;
const tFor = (locale: Locale) => (key: string) => lookupMessage(locale, key) ?? key;

describe("stage titles", () => {
  it("every stage of the graph has its title in English and Arabic", () => {
    const { stages, missing } = graphStages();
    // every stage the graph takes from a plugin's manifest is declared there, with its label
    expect(missing, "graph.py _fp() stages no first-party manifest declares").toEqual([]);
    // the whole graph, wherever each stage is declared (graph.py or a manifest): a parse that
    // lost one of the two sources would fall far below this
    expect(stages.size).toBeGreaterThan(50);
    expect(stages.get("thumb_small")).toBe("Small thumbnail");
    expect(stages.get("promo_links")).toBe("Promo links");
    for (const [stage, label] of stages) {
      const key = `queue.stage.${stage}.title`;
      // the English is the graph's label, so the page reads as it did
      expect(EN[key], `en ${key}`).toBe(label);
      expect(AR[key], `ar ${key}`).toMatch(/[؀-ۿ]/);
      expect(AR[key], `ar ${key}`).not.toBe(label);
    }
  });

  it("titles a stage in the reader's language, and keeps an unknown stage's label", () => {
    expect(stageTitle(tFor("ar"), "thumb_small", "Small thumbnail")).toBe(
      AR["queue.stage.thumb_small.title"]
    );
    expect(stageTitle(tFor("en"), "thumb_small", "Small thumbnail")).toBe("Small thumbnail");
    expect(stageTitle(tFor("ar"), "from_the_future", "Future step")).toBe("Future step");
  });

  it("titles every stage of a snapshot", () => {
    const s = {
      stages: [
        { stage: "probe", label: "Length" },
        { stage: "plex_x", label: "Plex X" },
      ] as StageInfo[],
    } as QueuesSnapshot;
    expect(titledSnapshot(tFor("ar"), s)!.stages.map((x) => x.label)).toEqual([
      AR["queue.stage.probe.title"],
      "Plex X",
    ]);
    expect(titledSnapshot(tFor("ar"), null)).toBeNull();
  });
});
