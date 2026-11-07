import { describe, expect, it } from "vitest";
import { lookupMessage, type Locale } from "../../lib/i18n";
import { reasonText } from "./componentWords";

// a t that fills {name} placeholders from the real message files
const tOf = (locale: Locale) => (key: string, vars?: Record<string, string | number>) => {
  const msg = lookupMessage(locale, key) ?? key;
  return msg.replace(/\{(\w+)\}/g, (m, k: string) =>
    vars && vars[k] !== undefined ? String(vars[k]) : m
  );
};
const titles: Record<string, string> = { llm: "LLM", "model.llm.qwen": "Qwen model" };
const titleOf = (id: string) => titles[id] ?? id;

describe("reasonText", () => {
  it("names the component the supervisor puts in the params", () => {
    // the supervisor writes dependency_off bare, with {component: id}
    const text = reasonText(tOf("en"), "en", "dependency_off", titleOf, { component: "llm" });
    expect(text).toBe("Needs LLM, which is off.");
  });

  it("names every component the runners list in {components}", () => {
    const text = reasonText(tOf("en"), "en", "dependency_off", titleOf, {
      components: ["llm", "model.llm.qwen"],
    });
    expect(text).toContain("LLM");
    expect(text).toContain("Qwen model");
    expect(text).not.toContain("{");
  });

  it("still reads the id after the colon, and says nothing without one", () => {
    expect(reasonText(tOf("en"), "en", "dependency_off:llm", titleOf)).toBe(
      "Needs LLM, which is off."
    );
    expect(reasonText(tOf("en"), "en", "dependency_off", titleOf)).toBe("");
  });

  it("names the component in Arabic too", () => {
    const text = reasonText(tOf("ar"), "ar", "dependency_off", titleOf, { component: "llm" });
    expect(text).toContain("LLM");
    expect(text).not.toBe("");
  });
});

describe("a plugin entry's words (Epic 54)", () => {
  it("are its own name and summary in the page's language, English when it has none", async () => {
    const { descText, titleFinder } = await import("./componentWords");
    const v = {
      id: "plugin.dev.example.x",
      title_key: "plugin.dev.example.x.name",
      desc_key: "plugin.dev.example.x.summary",
      names: { name: { en: "Tagger", ar: "الواسم" }, summary: { en: "Adds tags.", ar: "" } },
    } as never;
    expect(titleFinder(tOf("ar"), [v], "ar")("plugin.dev.example.x")).toBe("الواسم");
    expect(titleFinder(tOf("en"), [v])("plugin.dev.example.x")).toBe("Tagger");
    expect(descText(tOf("ar"), v, "ar")).toBe("Adds tags.");
  });
});
