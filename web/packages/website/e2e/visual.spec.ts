// Story 60.04 T08, the visual references: every page (the hand-made ones, every install guide,
// the generated families' representatives) at 360, 768 and 1280 px, English and Arabic, dark;
// each hand-made page also light at 1280. Full page, animations off, the date frozen, fonts
// loaded first; compared by pixels (maxDiffPixelRatio 0.001, playwright.config.ts). On a
// mismatch the expected, actual and diff images land in test-results/ (website-visual-diff-<shard>).
//
// The references are rendered only in the CI runner's image (WEBSITE_VISUAL=1, set by
// _website.yml's container job and scripts/e2e-update.sh), never on a Mac: elsewhere the spec
// skips, saying so.
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { label, PKG, settle, sitePages, type SitePage } from "./pages";

export const WIDTHS = [360, 768, 1280];
const FROZEN = new Date("2026-01-01T09:00:00Z");

interface Representatives {
  [family: string]: Record<string, string>;
}

/** The pages with references: hand-made, the install guides, and each other family's
 * representatives (e2e/representatives.json). */
export function visualPages(): SitePage[] {
  const reps: Representatives = JSON.parse(
    fs.readFileSync(path.join(PKG, "e2e/representatives.json"), "utf8")
  );
  const chosen = new Set(
    Object.entries(reps)
      .filter(([family]) => family !== "install" && !family.startsWith("$"))
      .flatMap(([, r]) => Object.values(r))
  );
  return sitePages().filter((p) => {
    if (p.kind === "hand-made" || p.kind === "install") return true;
    const en = p.lang === "ar" ? p.url.slice(3) : p.url;
    return chosen.has(en);
  });
}

/** Pages whose length is the registry's (the problem index lists every entry, story 60.06): their
 * reference is the first screen, with the list and its count masked, so it holds the template
 * (title, lead, filter) and a new registry entry changes no reference. Their every entry is
 * checked by the structural tests instead (README §11 decision 11). */
export const FIRST_SCREEN: Record<string, string[]> = {
  "/problems/": [".areas", "#count"],
};

/** A reference's file name: <page>-<width>-<lang>[-light].png */
export const shotName = (p: SitePage, width: number, light = false) =>
  `${p.name}-${width}-${p.lang}${light ? "-light" : ""}.png`;

const inImage = process.env.WEBSITE_VISUAL === "1" && process.platform === "linux";

for (const p of visualPages()) {
  test.describe(label(p), () => {
    test.skip(
      !inImage,
      "visual references render only in the CI runner's image (WEBSITE_VISUAL=1 on Linux: _website.yml, or npm run e2e:update)"
    );

    const shots: { width: number; light: boolean }[] = WIDTHS.map((width) => ({
      width,
      light: false,
    }));
    if (p.kind === "hand-made") shots.push({ width: 1280, light: true });

    for (const s of shots) {
      test(`T08: matches ${shotName(p, s.width, s.light)}`, async ({ page }) => {
        await page.clock.setFixedTime(FROZEN);
        await page.emulateMedia({
          colorScheme: s.light ? "light" : "dark",
          reducedMotion: "reduce",
        });
        await page.setViewportSize({ width: s.width, height: 800 });
        await page.goto(p.url);
        await settle(page);
        const en = p.lang === "ar" ? p.url.slice(3) : p.url;
        const firstScreen = FIRST_SCREEN[en];
        await expect(page).toHaveScreenshot(shotName(p, s.width, s.light), {
          fullPage: !firstScreen,
          mask: (firstScreen ?? []).map((sel) => page.locator(sel)),
          animations: "disabled",
          caret: "hide",
        });
      });
    }
  });
}
