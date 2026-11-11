// maktaba.tv/install/nas (story 59.14): the model table filled in the page's language, every
// model a row, and nothing fetched.
import { afterEach, describe, expect, it, vi } from "vitest";
import { MODELS } from "./nasModels";
import { fitText, renderModels, rowCells } from "./nasPage";

function doc(lang: "en" | "ar"): Document {
  const d = document.implementation.createHTMLDocument("t");
  d.documentElement.lang = lang;
  d.body.innerHTML = '<table><tbody id="models"></tbody></table><p id="models-note">n</p>';
  return d;
}

const model = (n: string) => MODELS.find((m) => m.name === n)!;

describe("the NAS landing page's model table", () => {
  afterEach(() => vi.restoreAllMocks());

  it.each(["en", "ar"] as const)("has a row per model in %s, and fetches nothing", (lang) => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const d = doc(lang);
    renderModels(d);
    const rows = d.querySelectorAll("#models tr");
    expect(rows).toHaveLength(MODELS.length);
    expect(rows[0]!.querySelectorAll("td")).toHaveLength(5);
    expect(d.getElementById("models-note")!.textContent).toMatch(/\d{4}-\d{2}-\d{2}/);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("says each fit in both languages", () => {
    expect(fitText("en", model("DS224+"))).toBe("Fits with more memory: Light at 6 GB");
    expect(fitText("ar", model("DS224+"))).toBe("مناسب بذاكرة أكبر: خفيف عند 6 غيغابايت");
    expect(fitText("en", model("DS223j"))).toBe("Use as storage");
    expect(fitText("ar", model("DXP2800"))).toBe("مناسب: قياسي");
    expect(rowCells("en", model("DS923+"))).toEqual([
      "Synology DS923+",
      model("DS923+").cpu,
      "Software",
      "4 (32)",
      "Fits: Light",
    ]);
  });
});
