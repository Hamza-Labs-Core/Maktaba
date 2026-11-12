// Story 60.04 T08, the visual references: every page (the hand-made ones, every install guide,
// the generated families' representatives) at 360, 768 and 1280 px, English and Arabic, dark;
// each hand-made page also light at 1280; a page built from the latest release with the saved
// release's values (RELEASE_DATA). Full page, animations off, the date frozen, fonts
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
// @ts-ignore: plain JavaScript build scripts
import * as dl from "../scripts/download.mjs";

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

/**
 * Pages built from data that changes on its own: their reference holds the template, drawn with
 * saved data. The download page (story 60.11) is built from the latest release, which moves with
 * every tag; each value it takes from the release sits in an element marked data-release, and
 * before the shot each is drawn with the saved release's (scripts/fixtures/release/, the unit
 * tests' too). A built page whose cards differ from the saved release's fails, naming both.
 */
export const RELEASE_DATA: Record<string, (lang: "en" | "ar") => Promise<string>> = {
  "/download/": async (lang) => {
    const got = await dl.readRelease({ env: { WEBSITE_RELEASE_FIXTURE: dl.FIXTURE } });
    const data = dl.releaseData(got);
    return dl.renderBody(data, dl.cards(data, dl.readPlatforms(), {}), {
      lang,
      manifest: dl.readManifest(),
    });
  },
};

/** Draws each data-release element of the page with the saved data's (RELEASE_DATA). */
async function drawSaved(page: import("@playwright/test").Page, html: string): Promise<void> {
  const problem = await page.evaluate((saved) => {
    const doc = new DOMParser().parseFromString(saved, "text/html");
    const want = new Map(
      [...doc.querySelectorAll<HTMLElement>("[data-release]")].map((e) => [
        e.dataset.release!,
        e.innerHTML,
      ])
    );
    const have = [...document.querySelectorAll<HTMLElement>("[data-release]")];
    // the same values, in any order: the page puts the visitor's platform card first (its head
    // script, by user agent), so the order follows the browser, not the release
    const keys = have.map((e) => e.dataset.release!).sort();
    const savedKeys = [...want.keys()].sort();
    if (keys.length !== new Set(keys).size || keys.join() !== savedKeys.join())
      return `the built page's release values (${keys.join(", ")}) are not the saved release's (${savedKeys.join(", ")})`;
    for (const e of have) e.innerHTML = want.get(e.dataset.release!)!;
    return "";
  }, html);
  expect(problem, "the built release's cards are the saved release's").toBe("");
}

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
        if (RELEASE_DATA[en]) await drawSaved(page, await RELEASE_DATA[en](p.lang));
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
