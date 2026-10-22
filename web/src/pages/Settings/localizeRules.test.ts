import { describe, expect, it } from "vitest";
import { localizeSections } from "./RulesSection";
import type { RuleSection } from "../../lib/rulesApi";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
// The live schema the API serves, read from disk at run time: the web image builds from web/
// alone, so a static import of a file outside it fails tsc there (hence no typed node imports).
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};
const schema = JSON.parse(
  readFileSync("../api/internal/handlers/rules/schema.json", "utf8") // vitest runs in web/
) as { sections: RuleSection[] };

const sections = (): RuleSection[] =>
  schema.sections.map((s) => ({
    ...s,
    fields: s.fields.map((f) => ({ ...f, value: f.default, changed: false })),
  }));

describe("localizeSections", () => {
  it("shows the Maktaba TV output section in Arabic and leaves names and choices", () => {
    const out = localizeSections(sections(), "ar").find((s) => s.key === "tv_output")!;
    expect(out.title).toBe("إخراج تلفزيون مكتبة");
    const enc = out.fields.find((f) => f.name === "encoder")!;
    expect(enc.label).toBe("مُرمِّز الفيديو");
    expect(enc.choices).toEqual(["auto", "nvenc", "videotoolbox", "software"]);
    expect(out.fields.find((f) => f.name === "nvenc_max_sessions")!.group).toBe(
      "المُرمِّز العتادي"
    );
    const tv = localizeSections(sections(), "ar").find((s) => s.key === "tv")!;
    expect(tv.fields.find((f) => f.name === "horizon_days")!.label).toBe("أيام الدليل القادمة");
  });

  it("keeps the schema's English where the catalog has nothing", () => {
    const raw = sections();
    const out = localizeSections(raw, "ar");
    const planner = out.find((s) => s.key === "planner")!;
    expect(planner.title).toBe(raw.find((s) => s.key === "planner")!.title);
  });

  it("matches the schema in English (the catalog copy is the schema's)", () => {
    const raw = sections();
    const tvo = raw.find((s) => s.key === "tv_output")!;
    for (const f of tvo.fields) {
      const label = (en as Record<string, string>)[`rules.tv_output.${f.name}.label`];
      expect(label, f.name).toBe(f.label);
    }
    expect(tvo.fields.map((f) => f.name)).toEqual([
      "encoder",
      "resolution",
      "fps",
      "video_kbps",
      "audio_codec",
      "audio_kbps",
      "segment_seconds",
      "keyint_seconds",
      "max_active_channels",
      "warm_grace_seconds",
      "nvenc_max_sessions",
      "videotoolbox_max_sessions",
      "unreadable_file_fill",
      "source_timeout_seconds",
      "source_stall_seconds",
      "report_unreadable_files",
    ]);
  });

  // Epic 32 story 32.12 AC2 / TC2: every TV cluster and remote setting is
  // described in both languages, the English copy being the schema's.
  it("describes every tv_cluster and tv_remote setting in English and Arabic", () => {
    const enC = en as Record<string, string>;
    const arC = ar as Record<string, string>;
    for (const key of ["tv_cluster", "tv_remote"]) {
      const sec = sections().find((s) => s.key === key)!;
      expect(sec, key).toBeTruthy();
      expect(arC[`rules.${key}.title`], `${key} title`).toBeTruthy();
      expect(arC[`rules.${key}.what`], `${key} what`).toBeTruthy();
      for (const f of sec.fields) {
        const k = `rules.${key}.${f.name}`;
        expect(enC[`${k}.label`], `${k}.label (en)`).toBe(f.label);
        expect(arC[`${k}.label`], `${k}.label (ar)`).toBeTruthy();
        expect(arC[`rules.group.${f.group}`], `group ${f.group} (ar)`).toBeTruthy();
        if (f.help) {
          expect(enC[`${k}.help`], `${k}.help (en)`).toBe(f.help);
          expect(arC[`${k}.help`], `${k}.help (ar)`).toBeTruthy();
        }
      }
    }
  });

  // Every rule shows in Arabic on the Settings page: its section's title and summary, and
  // each field's label, help and group. The sections below came before this check and still
  // show their English; one leaves the list once translated, and a new one never joins it.
  const NOT_YET_IN_ARABIC = new Set([
    "planner",
    "groups",
    "detect",
    "tag",
    "tagcheck",
    "books",
    "dizquetv",
    "tv",
    "tv_simulcast",
    "exactdates",
    "audit",
    "plexlive",
    "repair",
    "relay",
    "storage",
    "updates",
    "queue_health",
    "cluster",
  ]);
  const withoutArabic = (sec: RuleSection): string[] => {
    const arC = ar as Record<string, string>;
    const keys = [`rules.${sec.key}.title`, `rules.${sec.key}.what`];
    for (const f of sec.fields) {
      keys.push(`rules.${sec.key}.${f.name}.label`, `rules.group.${f.group}`);
      if (f.help) keys.push(`rules.${sec.key}.${f.name}.help`);
    }
    return keys.filter((k) => !arC[k]);
  };

  it("describes every rule in Arabic, but the sections not yet translated", () => {
    for (const sec of sections()) {
      if (NOT_YET_IN_ARABIC.has(sec.key)) continue;
      expect(withoutArabic(sec), `rules ${sec.key}: no Arabic text`).toEqual([]);
    }
  });

  it("lists only sections that are still without their Arabic", () => {
    const keys = new Set(sections().map((s) => s.key));
    for (const key of NOT_YET_IN_ARABIC) {
      expect(keys.has(key), `${key}: no such rules section`).toBe(true);
      const sec = sections().find((s) => s.key === key)!;
      expect(
        withoutArabic(sec).length,
        `${key} is in Arabic now: take it off the list`
      ).toBeGreaterThan(0);
    }
  });

  it("shows the transcription rules in Arabic", () => {
    const sec = localizeSections(sections(), "ar").find((s) => s.key === "transcription")!;
    expect(sec.title).toBe("التفريغ النصي");
    const f = sec.fields.find((x) => x.name === "without_second_listen")!;
    expect(f.label).toBe("التفريغ دون الاستماع الثاني");
    expect(f.help).toContain("الاستماع الثاني");
    expect(f.group).toBe("التفريغ النصي");
  });
});
