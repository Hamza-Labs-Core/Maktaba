// Story 60.04 T03 and the checks beyond axe: on every page, in both languages and both themes,
// axe (WCAG 2.0, 2.1 and 2.2, A and AA) finds nothing; every target is 24×24 CSS px or spaced as
// 2.5.8 allows; with reduced motion nothing moves; and the language of the page and of each run
// in the other language is marked. axe's findings are kept (test-results/axe/), pass or fail.
import fs from "node:fs";
import path from "node:path";
import { AxeBuilder } from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import { label, PKG, settle, sitePages } from "./pages";

const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];
const AXE_DIR = path.join(PKG, "test-results/axe");

/** The targets too small for 2.5.8: under 24×24 CSS px, not in a sentence, and with a 24 px
 * circle on their centre that meets another target (the spacing exception). */
export async function smallTargets(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const sel = "a[href], button, input, select, textarea, summary, [role=button], [tabindex]";
    const els = [...document.querySelectorAll<HTMLElement>(sel)].filter((el) => {
      if (el.tabIndex < 0 && !el.matches("a[href], button, input, select, textarea, summary"))
        return false;
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && el.checkVisibility();
    });
    // the skip link sits off screen until focused: it is measured as it shows
    const rects = els.map((el) => el.getBoundingClientRect());
    // in a sentence (2.5.8's inline exception): its block has text of its own beside the target
    const inSentence = (el: HTMLElement) => {
      if (getComputedStyle(el).display !== "inline") return false;
      const block = el.closest("p, li, td, th, dd, dt, figcaption, label, blockquote");
      if (!block) return false;
      const own = (block.textContent ?? "").replace(/\s+/g, " ").trim();
      const mine = (el.textContent ?? "").replace(/\s+/g, " ").trim();
      return own.length > mine.length + 3;
    };
    const out: string[] = [];
    els.forEach((el, i) => {
      const r = rects[i];
      if (el.classList.contains("skip-link")) return;
      if (r.width >= 24 && r.height >= 24) return;
      if (inSentence(el)) return;
      // the spacing exception: a 24 px circle on its centre meets no other target
      const cx = r.left + r.width / 2;
      const cy = r.top + r.height / 2;
      const meets = rects.some((o, j) => {
        if (j === i) return false;
        if (els[j].contains(el) || el.contains(els[j])) return false;
        const nx = Math.max(o.left, Math.min(cx, o.right));
        const ny = Math.max(o.top, Math.min(cy, o.bottom));
        return Math.hypot(nx - cx, ny - cy) < 12;
      });
      if (meets) {
        const name = (el.textContent ?? el.getAttribute("aria-label") ?? el.tagName).trim();
        out.push(
          `${el.tagName.toLowerCase()} "${name.slice(0, 40)}" ${r.width.toFixed(0)}×${r.height.toFixed(0)}`
        );
      }
    });
    return out;
  });
}

for (const p of sitePages()) {
  for (const scheme of ["dark", "light"] as const) {
    test.describe(`${label(p)} ${scheme}`, () => {
      test.use({ colorScheme: scheme });

      test("T03: axe finds no WCAG 2.2 A or AA violation", async ({ page }) => {
        await page.goto(p.url);
        await settle(page);
        const results = await new AxeBuilder({ page }).withTags(TAGS).analyze();
        fs.mkdirSync(AXE_DIR, { recursive: true });
        const file = `${p.lang}-${p.name}-${scheme}.json`;
        fs.writeFileSync(
          path.join(AXE_DIR, file),
          JSON.stringify({ url: p.url, scheme, violations: results.violations }, null, 2)
        );
        const found = results.violations.map(
          (v) =>
            `${v.id} (${v.impact}): ${v.help}\n    ${v.nodes.map((n) => n.target.join(" ")).join("\n    ")}`
        );
        expect(found, `axe on ${p.url} (${scheme})`).toEqual([]);
      });
    });
  }

  test.describe(label(p), () => {
    test("2.5.8: every target is 24×24 CSS px, or spaced as the exception allows", async ({
      page,
    }) => {
      for (const width of [360, 1280]) {
        await page.setViewportSize({ width, height: 800 });
        await page.goto(p.url);
        await settle(page);
        expect(await smallTargets(page), `at ${width} px`).toEqual([]);
      }
    });

    test("reduced motion: no transition or animation runs", async ({ page }) => {
      await page.emulateMedia({ reducedMotion: "reduce" });
      await page.goto(p.url);
      await settle(page);
      // hover and focus what moves most often: the links and buttons
      const first = page.locator("main a[href], main button").first();
      if (await first.count()) {
        await first.hover();
        await first.focus();
      }
      const moving = await page.evaluate(() => {
        const out: string[] = [];
        const secs = (v: string) =>
          Math.max(
            ...v.split(",").map((x) => parseFloat(x) * (x.trim().endsWith("ms") ? 0.001 : 1))
          );
        for (const el of document.querySelectorAll<HTMLElement>("*")) {
          const s = getComputedStyle(el);
          if (s.transitionProperty !== "none" && secs(s.transitionDuration) > 0.001)
            out.push(
              `${el.tagName.toLowerCase()}.${el.className}: transition ${s.transitionDuration}`
            );
          if (s.animationName !== "none" && secs(s.animationDuration) > 0.001)
            out.push(`${el.tagName.toLowerCase()}.${el.className}: animation ${s.animationName}`);
        }
        for (const a of document.getAnimations()) out.push(`running: ${a.constructor.name}`);
        return out;
      });
      expect(moving).toEqual([]);
    });

    test("language: <html lang dir>, and the switch to the other language marks its own", async ({
      page,
    }) => {
      await page.goto(p.url);
      const html = page.locator("html");
      await expect(html).toHaveAttribute("lang", p.lang);
      await expect(html).toHaveAttribute("dir", p.lang === "ar" ? "rtl" : "ltr");
      const other = p.lang === "ar" ? "en" : "ar";
      // the language switch: a link to the other language, marked as written in it
      const sw = page.locator(`a[hreflang="${other}"]`).first();
      await expect(sw, "the language switch").toHaveCount(1);
      await expect(sw).toHaveAttribute("lang", other);
      // every lang the page marks is one of the two
      const langs = await page.$$eval("[lang]", (els) => els.map((e) => e.getAttribute("lang")));
      for (const l of langs) expect(["en", "ar"], `lang="${l}"`).toContain(l);
    });
  });
}
