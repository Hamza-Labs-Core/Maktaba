// Story 60.04 T06, RTL mirroring as a test: every Arabic page is right to left (dir="rtl", and
// the computed direction of <main> is rtl); an element marked data-mirror (an arrow that means
// "next") is mirrored there, by a flipped transform or a glyph of the other direction; an element
// marked data-nomirror (logos, platform marks, media controls, screenshots, code, checkmarks)
// keeps its transform. On English pages neither is flipped. Two fixtures show the check fails
// when a marker is wrong (positive controls).
import { expect, test, type Page } from "@playwright/test";
import { label, settle, sitePages } from "./pages";

/** The mirror markers that are wrong on the page, for its direction. */
async function wrongMirrors(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const rtl = getComputedStyle(document.documentElement).direction === "rtl";
    // arrows drawn as text: which way each one points
    const LEFTWARD = /[←‹«◀⟵⇐]/;
    const RIGHTWARD = /[→›»▶⟶⇒]/;
    const flipped = (el: Element) => {
      const t = getComputedStyle(el).transform;
      if (t === "none") return false;
      const m = new DOMMatrixReadOnly(t);
      return m.a < 0;
    };
    const glyph = (el: Element) => {
      const text = el.textContent ?? "";
      if (LEFTWARD.test(text)) return "left";
      if (RIGHTWARD.test(text)) return "right";
      return null;
    };
    const name = (el: Element) =>
      `${el.tagName.toLowerCase()} "${(el.textContent ?? "").trim().slice(0, 30)}"`;
    const out: string[] = [];
    for (const el of document.querySelectorAll("[data-mirror]")) {
      // the mark points forward: right in English, left in Arabic, by a transform or a glyph
      const g = glyph(el);
      const forward = g ? (g === "left") === rtl : flipped(el) === rtl;
      if (!forward) out.push(`${name(el)}: data-mirror, not mirrored for ${rtl ? "rtl" : "ltr"}`);
    }
    for (const el of document.querySelectorAll("[data-nomirror]"))
      if (flipped(el)) out.push(`${name(el)}: data-nomirror, but flipped`);
    return out;
  });
}

test.describe("positive controls", () => {
  test("an Arabic arrow left unmirrored is found", async ({ page }) => {
    await page.setContent(
      `<html lang="ar" dir="rtl"><body><a data-mirror href="#">التالي <span>→</span></a></body></html>`
    );
    expect(await wrongMirrors(page)).toHaveLength(1);
    // mirrored right: an icon flipped by CSS, or an arrow glyph of the other direction
    await page.setContent(
      `<html lang="ar" dir="rtl"><body><i data-mirror style="display:inline-block;transform:scaleX(-1)"></i><a data-mirror>← التالي</a></body></html>`
    );
    expect(await wrongMirrors(page)).toEqual([]);
  });

  test("a logo flipped in Arabic is found", async ({ page }) => {
    await page.setContent(
      `<html lang="ar" dir="rtl"><body><img data-nomirror alt="" width="10" height="10" style="transform:scaleX(-1)"></body></html>`
    );
    expect(await wrongMirrors(page)).toHaveLength(1);
  });
});

for (const p of sitePages()) {
  test.describe(label(p), () => {
    test("T06: direction, and the mirror markers", async ({ page }) => {
      await page.goto(p.url);
      await settle(page);
      const dir = p.lang === "ar" ? "rtl" : "ltr";
      await expect(page.locator("html")).toHaveAttribute("dir", dir);
      const main = await page.evaluate(() => {
        const m = document.querySelector("main");
        return m ? getComputedStyle(m).direction : null;
      });
      expect(main, "the computed direction of <main>").toBe(dir);
      expect(await wrongMirrors(page)).toEqual([]);
    });
  });
}
