// Every stage the pipeline graph ships (engine/graph.py GRAPH, the legacy stages too) has
// its title in English (the graph's own label) and in Arabic, so a new stage without words
// fails here; a stage with no words keeps the label the API sent.
import { describe, expect, it } from "vitest";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";
import { lookupMessage, type Locale } from "./i18n";
import { stageTitle, titledSnapshot } from "./stageWords";
import type { QueuesSnapshot, StageInfo } from "./queueApi";

// the graph is outside src/: read it at run time, as help.coverage.test.ts does
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};

/** name -> label of each stage in the graph: StageDef(name=..., label=...) and _legacy(...). */
function graphStages(): Map<string, string> {
  const src = readFileSync("../pipeline/src/maktaba_pipeline/engine/graph.py", "utf8");
  const out = new Map<string, string>();
  for (const m of src.matchAll(/\bname="([a-z0-9_]+)",\s*label="([^"]+)"/g)) out.set(m[1]!, m[2]!);
  for (const m of src.matchAll(/_legacy\(\s*"([a-z0-9_]+)",\s*"([^"]+)"/g)) out.set(m[1]!, m[2]!);
  return out;
}

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;
const tFor = (locale: Locale) => (key: string) => lookupMessage(locale, key) ?? key;

describe("stage titles", () => {
  it("every stage of the graph has its title in English and Arabic", () => {
    const stages = graphStages();
    // a floor that catches a pattern that no longer matches, not the count itself: Epic 54's
    // Phase 4 moves stages out of the graph into first-party plugins
    expect(stages.size).toBeGreaterThan(30);
    expect(stages.get("thumb_small")).toBe("Small thumbnail");
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
