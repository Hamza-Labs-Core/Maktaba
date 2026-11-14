// Story 58.15: one reading order on every TV. The web TVs (/tv/, the launcher, the Cast
// receiver) let the browser shape Arabic and order it by the bidi algorithm; Roku draws the
// visual string VisualText builds, checked against visual-vectors.json. This module lets a
// browser test hold the browser's result against the same vectors:
//
//   const got = await page.locator(".mk-title").evaluate(visualOrder);
//   expect(got).toBe(expectedOrder("episode.06"));
//
// visualOrder runs in the page: it reads where each character of an element's text is drawn
// and returns the characters from left to right (the bidi marks, which draw nothing, left out).
// expectedOrder is the vector's visual string with its presentation forms folded back to the
// letters (NFKC), the same characters from left to right. Equal strings mean the same reading
// order; arabicFontLoaded checks the joined letters come from a bundled Arabic font.
// The text must fit on one line.

import { readFileSync } from "node:fs";

const VECTORS = new URL("./visual-vectors.json", import.meta.url);

/** In the page: the element's characters as drawn, from left to right. */
export function visualOrder(el) {
  const MARKS = /[؜‎‏‪-‮⁦-⁩]/;
  const drawn = [];
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const text = n.textContent ?? "";
    for (let i = 0; i < text.length; i++) {
      if (MARKS.test(text[i])) continue;
      const range = document.createRange();
      range.setStart(n, i);
      range.setEnd(n, i + 1);
      const box = range.getBoundingClientRect();
      if (box.width === 0 && box.height === 0) continue;
      drawn.push({ ch: text[i], x: box.left + box.width / 2, top: box.top });
    }
  }
  const tops = new Set(drawn.map((d) => Math.round(d.top / 4)));
  if (tops.size > 1)
    throw new Error(`visualOrder: the text wraps (${tops.size} lines)`);
  drawn.sort((a, b) => a.x - b.x);
  return drawn.map((d) => d.ch).join("");
}

/**
 * In the page: whether the element's own font (its first family) is a bundled web font with a
 * loaded face covering each of the element's Arabic letters. A browser joins Arabic only in a
 * font that has the forms. (document.fonts.check is not used: it says yes when the family has
 * no face at all, and no while an unused duplicate of a loaded face is still unloaded.)
 */
export function arabicFontLoaded(el) {
  const family = getComputedStyle(el)
    .fontFamily.split(",")[0]
    .trim()
    .replace(/^["']|["']$/g, "");
  const letters = [...(el.textContent ?? "")].filter((c) =>
    /[\u0600-\u06ff]/.test(c),
  );
  const ranges = [...document.fonts]
    .filter(
      (f) =>
        f.family.replace(/^["']|["']$/g, "") === family &&
        f.status === "loaded",
    )
    .flatMap((f) => f.unicodeRange.split(","))
    .map((r) => {
      const [lo, hi] = r.trim().replace(/^U\+/i, "").split("-");
      return [parseInt(lo, 16), parseInt(hi ?? lo, 16)];
    });
  const covered = (c) =>
    ranges.some(([lo, hi]) => c.codePointAt(0) >= lo && c.codePointAt(0) <= hi);
  return letters.length > 0 && letters.every(covered);
}

/** The vector's visual string, as letters from left to right. */
export function expectedOrder(id) {
  const { vectors } = JSON.parse(readFileSync(VECTORS, "utf8"));
  const v = vectors.find((x) => x.id === id);
  if (!v) throw new Error(`no vector ${id} in visual-vectors.json`);
  return v.visual.normalize("NFKC");
}

/** The vector's logical input: the title the mock home sends. */
export function vectorInput(id) {
  const { vectors } = JSON.parse(readFileSync(VECTORS, "utf8"));
  const v = vectors.find((x) => x.id === id);
  if (!v) throw new Error(`no vector ${id} in visual-vectors.json`);
  return v.input;
}
