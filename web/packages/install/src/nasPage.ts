// maktaba.tv/install/nas's model table (Epic 59 story 59.14): one row per model of
// nas-models.json, in the page's language (<html lang>). Everything else on the page is static.

import { CHECKED, MODELS, fitOf, type Model } from "./nasModels";

export type Lang = "en" | "ar";

const T = {
  en: {
    hw: "Hardware",
    sw: "Software",
    light: "Light",
    standard: "Standard",
    strong: "Strong",
    fits: "Fits: {tier}",
    more: "Fits with more memory: {tier} at {max} GB",
    storage: "Use as storage",
    checked: "The table was last checked on {date}.",
    vendor: { synology: "Synology", qnap: "QNAP", ugreen: "UGREEN", asustor: "ASUSTOR", other: "" },
  },
  ar: {
    hw: "عتادي",
    sw: "برمجي",
    light: "خفيف",
    standard: "قياسي",
    strong: "قوي",
    fits: "مناسب: {tier}",
    more: "مناسب بذاكرة أكبر: {tier} عند {max} غيغابايت",
    storage: "استخدمه للتخزين",
    checked: "رُوجع الجدول آخر مرة في {date}.",
    vendor: { synology: "Synology", qnap: "QNAP", ugreen: "UGREEN", asustor: "ASUSTOR", other: "" },
  },
} as const;

/** The "With Maktaba" cell of a model. */
export function fitText(lang: Lang, m: Model): string {
  const t = T[lang];
  const f = fitOf(m);
  if (f.kind === "storage") return t.storage;
  if (f.kind === "fits") return t.fits.replace("{tier}", t[f.tier]);
  return t.more.replace("{tier}", t[f.tier]).replace("{max}", String(f.maxRam));
}

/** A row's cells: the model (with its maker), the processor, the video, the memory it ships
 * with (and the most it takes), and the fit. */
export function rowCells(lang: Lang, m: Model): string[] {
  const t = T[lang];
  const maker = t.vendor[m.vendor];
  return [
    maker ? `${maker} ${m.name}` : m.name,
    m.cpu,
    m.gpu ? t.hw : t.sw,
    m.maxRam > m.ram ? `${m.ram} (${m.maxRam})` : String(m.ram),
    fitText(lang, m),
  ];
}

/** Fills the table's body and the note under it. */
export function renderModels(doc: Document): void {
  const lang: Lang = doc.documentElement.lang === "ar" ? "ar" : "en";
  const body = doc.getElementById("models");
  if (!body) return;
  for (const m of MODELS) {
    const tr = doc.createElement("tr");
    tr.dataset.fit = fitOf(m).kind;
    for (const [i, c] of rowCells(lang, m).entries()) {
      const td = doc.createElement("td");
      // a Latin name or a number inside Arabic text keeps its own direction
      if (i < 2 || i === 3) td.append(Object.assign(doc.createElement("bdi"), { textContent: c }));
      else td.textContent = c;
      tr.append(td);
    }
    body.append(tr);
  }
  const note = doc.getElementById("models-note");
  if (note) note.append(" ", T[lang].checked.replace("{date}", CHECKED));
}
