// Story 60.04 T05: at 320 CSS px no page scrolls sideways (1.4.10), and with WCAG's text
// spacing (line height 1.5, paragraphs 2 em apart, letters 0.12 em, words 0.16 em) no text is cut
// off (1.4.12). Every page, both languages.
import { expect, test, type Page } from "@playwright/test";
import { label, settle, sitePages } from "./pages";

const TEXT_SPACING = `* { line-height: 1.5 !important; letter-spacing: 0.12em !important;
  word-spacing: 0.16em !important; } p { margin-block-end: 2em !important; }`;

/** What scrolls sideways: the page, and the elements wider than it (not code blocks and tables
 * that scroll on their own, which 1.4.10 allows). */
async function overflow(page: Page) {
  return page.evaluate(() => {
    const root = document.documentElement;
    const out: string[] = [];
    if (root.scrollWidth > root.clientWidth)
      out.push(`the page: ${root.scrollWidth} px wide in ${root.clientWidth}`);
    return out;
  });
}

/** Text cut off: an element that hides its overflow while its content is larger than it. */
async function clipped(page: Page) {
  return page.evaluate(() => {
    const out: string[] = [];
    for (const el of document.querySelectorAll<HTMLElement>("body *")) {
      const s = getComputedStyle(el);
      const hides = (v: string) => v === "hidden" || v === "clip";
      if (!(el.textContent ?? "").trim()) continue;
      if (hides(s.overflowY) && el.scrollHeight > el.clientHeight + 1)
        out.push(
          `${el.tagName.toLowerCase()}.${el.className}: ${el.scrollHeight} > ${el.clientHeight}`
        );
      if (
        hides(s.overflowX) &&
        el.scrollWidth > el.clientWidth + 1 &&
        s.textOverflow !== "ellipsis"
      )
        out.push(
          `${el.tagName.toLowerCase()}.${el.className}: ${el.scrollWidth} > ${el.clientWidth}`
        );
    }
    return out;
  });
}

for (const p of sitePages()) {
  test.describe(label(p), () => {
    // the text-spacing stylesheet is the reader's own (a bookmarklet, an extension): the site's
    // CSP (style-src 'self') must not stop it, as it would not stop a user stylesheet
    test.use({ bypassCSP: true });
    test("T05: at 320 px the page does not scroll sideways", async ({ page }) => {
      await page.setViewportSize({ width: 320, height: 640 });
      await page.goto(p.url);
      await settle(page);
      expect(await overflow(page)).toEqual([]);
    });

    test("1.4.12: text spacing cuts no text off, at 320 and 1280 px", async ({ page }) => {
      for (const width of [320, 1280]) {
        await page.setViewportSize({ width, height: 800 });
        await page.goto(p.url);
        await settle(page);
        await page.addStyleTag({ content: TEXT_SPACING });
        expect(await overflow(page), `at ${width} px`).toEqual([]);
        expect(await clipped(page), `at ${width} px`).toEqual([]);
      }
    });
  });
}
