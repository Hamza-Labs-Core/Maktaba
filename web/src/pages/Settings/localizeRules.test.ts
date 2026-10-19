import { describe, expect, it } from "vitest";
import { localizeSections } from "./RulesSection";
import type { RuleSection } from "../../lib/rulesApi";
import en from "../../i18n/en.json";
import schema from "../../../../api/internal/handlers/rules/schema.json";

const sections = (): RuleSection[] =>
  (schema as { sections: RuleSection[] }).sections.map((s) => ({
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
    expect(out.fields.find((f) => f.name === "nvenc_max_sessions")!.group).toBe("المُرمِّز العتادي");
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
      "max_active_channels",
      "warm_grace_seconds",
      "nvenc_max_sessions",
      "videotoolbox_max_sessions",
    ]);
  });
});
