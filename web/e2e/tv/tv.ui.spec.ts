// Epic 58 story 58.08 — the TV build (/tv/) in Chromium at 1920×1080 against the mock
// home, driven with D-pad keys only (arrows, Enter, Back):
//
//   T03: each screen, dark, in English and Arabic (screenshots in test-results/tv/shots);
//        the console and the network log are attached to the report.
//   T04: Home → Library → Series with the CPU throttled ×6, each step under the
//        3-second budget between screens.
//   58.15: an Arabic title with a number and an English word reads in the order Roku draws it
//        (shared/i18n/visual-vectors.json), in the bundled Arabic font, in both languages.
import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { mkdirSync } from "node:fs";
import path from "node:path";
import {
  arabicFontLoaded,
  expectedOrder,
  vectorInput,
  visualOrder,
} from "../../../shared/i18n/visual_order.mjs";
import { mockHome, type MockHome } from "./mockHome";

const SHOTS = path.resolve(process.cwd(), "test-results/tv/shots");

/** The console and the network, kept for the report. */
function record(page: Page) {
  const consoleLines: string[] = [];
  const network: string[] = [];
  page.on("console", (m) => consoleLines.push(`[${m.type()}] ${m.text()}`));
  page.on("pageerror", (e) => consoleLines.push(`[pageerror] ${e.message}`));
  page.on("requestfinished", async (r) => {
    const res = await r.response();
    network.push(`${res?.status() ?? "-"} ${r.method()} ${r.url()}`);
  });
  page.on("requestfailed", (r) =>
    network.push(`FAILED ${r.method()} ${r.url()} ${r.failure()?.errorText ?? ""}`)
  );
  return {
    consoleLines,
    async attach(info: TestInfo, home?: MockHome) {
      await info.attach("console.log", {
        body: consoleLines.join("\n"),
        contentType: "text/plain",
      });
      await info.attach("network.log", { body: network.join("\n"), contentType: "text/plain" });
      if (home)
        await info.attach("home-calls.log", {
          body: home.calls.join("\n"),
          contentType: "text/plain",
        });
    },
  };
}

async function start(page: Page, locale: "en" | "ar") {
  await page.addInitScript((l) => {
    try {
      localStorage.setItem("mkt:locale", l);
    } catch {
      /* private mode: English */
    }
  }, locale);
}

const focused = (page: Page) =>
  page.evaluate(() => document.activeElement?.getAttribute("data-bs-focus") ?? "");

const OPPOSITE: Record<string, string> = {
  ArrowLeft: "ArrowRight",
  ArrowRight: "ArrowLeft",
  ArrowUp: "ArrowDown",
  ArrowDown: "ArrowUp",
};

/**
 * Presses `key` until the focus is on `id` (D-pad only); at the row's end (the
 * focus stops moving) it turns round, as a person would.
 */
async function pressTo(page: Page, key: string, id: string | RegExp, max = 16) {
  const hit = (f: string) => (typeof id === "string" ? f === id : id.test(f));
  let k = key;
  let turned = false;
  let last = await focused(page);
  for (let i = 0; i < max; i++) {
    if (hit(last)) return;
    await page.keyboard.press(k);
    await page.waitForTimeout(80);
    const now = await focused(page);
    if (now === last && !turned && OPPOSITE[k]) {
      k = OPPOSITE[k]!;
      turned = true;
    }
    last = now;
  }
  throw new Error(`focus never reached ${id} with ${key} (on ${last})`);
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  // the fonts and the posters first: a screenshot of what a person sees
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
}

test("signed out: the sign-in page sends the TV to the home's sign-in", async ({ page }, info) => {
  const log = record(page);
  const home = await mockHome(page, { signedIn: false });
  await start(page, "en");
  await page.goto("/tv/");
  await expect(page.getByTestId("tv-signin")).toBeVisible();
  await shot(page, "signin-en");
  await page.keyboard.press("Enter");
  await page.waitForURL(/\/login\?next=%2Ftv%2F/);
  await log.attach(info, home);
});

for (const locale of ["en", "ar"] as const) {
  test(`every screen with the D-pad only, dark, ${locale}`, async ({ page }, info) => {
    const log = record(page);
    const home = await mockHome(page);
    await start(page, locale);
    // the start: Ink & Brass, the language's direction
    await page.goto("/tv/");
    await expect(page).toHaveURL(/\/tv\/big$/);
    await expect(page.getByTestId("big-hero")).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("dir", locale === "ar" ? "rtl" : "ltr");
    // the TV build has no Leave big screen button
    await expect(page.getByTestId("big-leave")).toHaveCount(0);
    await shot(page, `home-${locale}`);

    const forward = locale === "ar" ? "ArrowLeft" : "ArrowRight";
    const backward = locale === "ar" ? "ArrowRight" : "ArrowLeft";

    // Libraries: up to the tab row, along it in reading order (mirrored in Arabic)
    await pressTo(page, "ArrowUp", /^tab:/);
    await pressTo(page, forward, "tab:/big/libraries");
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("big-libraries")).toBeVisible();
    await pressTo(page, "ArrowDown", /^lib:/);
    await pressTo(page, forward, "lib:L1");
    await shot(page, `libraries-${locale}`);

    // a library: Recommended first
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/tv\/big\/library\/L1/);
    await expect(page.locator('[data-bs-focus="ltab:recommended"]')).toBeVisible();
    await shot(page, `library-recommended-${locale}`);

    // the Library tab: the grid and the letter rail (ا…ي first in Arabic)
    await pressTo(page, "ArrowUp", /^ltab:/);
    await pressTo(page, forward, "ltab:library");
    await expect(page.getByTestId("big-letter-rail")).toBeVisible();
    const letters = await page
      .getByTestId("big-letter-rail")
      .locator("[data-bs-focus^='letter:']:not([data-bs-focus='letter:all'])")
      .evaluateAll((els) =>
        els.map((e) => e.getAttribute("data-bs-focus")!.slice("letter:".length))
      );
    expect(letters[0]).toBe(locale === "ar" ? "ا" : "A");
    await shot(page, `library-grid-${locale}`);

    // Collections and Playlists
    await pressTo(page, forward, "ltab:collections");
    await expect(page.getByText("Ramadan")).toBeVisible();
    await shot(page, `library-collections-${locale}`);
    await pressTo(page, forward, "ltab:playlists");
    await expect(page.getByText("Before sleep")).toBeVisible();
    await shot(page, `library-playlists-${locale}`);
    await pressTo(page, backward, "ltab:recommended");

    // Back: to the libraries, the one opened keeps the focus
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("big-libraries")).toBeVisible();

    // Series: the list, then a series with its season pills
    await pressTo(page, "ArrowUp", /^tab:/);
    await pressTo(page, forward, "tab:/big/series");
    await page.keyboard.press("Enter");
    await expect(page.locator('[data-bs-focus="series:S1"]')).toBeVisible();
    await pressTo(page, "ArrowDown", "series:S1");
    await page.keyboard.press("Enter");
    await expect(page.locator('[data-bs-focus="season:2"]')).toBeVisible();
    await shot(page, `series-${locale}`);

    // Back, step by step, to Home; then once more: Leave Maktaba? (Stay has the focus)
    for (let i = 0; i < 4 && !(await page.getByTestId("big-hero").isVisible()); i++) {
      await page.keyboard.press("Escape");
      await page.waitForTimeout(300);
    }
    await expect(page.getByTestId("big-hero")).toBeVisible();
    await page.keyboard.press("Escape");
    const dialog = page.getByTestId("big-exit");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(locale === "ar" ? "الخروج من Maktaba؟" : "Leave Maktaba?");
    expect(await focused(page)).toBe("exit-stay");
    await shot(page, `exit-${locale}`);
    await page.keyboard.press("Enter");
    await expect(dialog).toHaveCount(0);

    await log.attach(info, home);
    expect(home.unknown, "every call the screens made has an answer in the mock home").toEqual([]);
    expect(log.consoleLines.filter((l) => l.startsWith("[pageerror]"))).toEqual([]);
  });
}

// Story 58.15: "الحلقة 3 — Intro" reads Arabic first (on the right), then 3, the dash and Intro,
// as Roku's VisualText draws it (vector episode.06), whatever the screen's language: the title
// is isolated (<bdi>) and takes its direction from its first letter. Joined letters come from
// the bundled Arabic font (Amiri for headings), loaded.
for (const locale of ["en", "ar"] as const) {
  test(`an Arabic title reads in Roku's order, joined, ${locale} (58.15)`, async ({
    page,
  }, info) => {
    const log = record(page);
    const home = await mockHome(page, {
      rename: { "Opening the Book": vectorInput("episode.06") },
    });
    await start(page, locale);
    await page.goto("/tv/");
    const title = page.locator(".bs-hero__title");
    await expect(title).toHaveText(vectorInput("episode.06"));
    await page.evaluate(() => document.fonts.ready);
    expect(await title.evaluate(visualOrder)).toBe(expectedOrder("episode.06"));
    // a face loads when text first needs it, which can be after document.fonts.ready
    await expect
      .poll(() => title.evaluate(arabicFontLoaded), { message: "the title's bundled Arabic font" })
      .toBe(true);
    await shot(page, `arabic-title-${locale}`);
    await log.attach(info, home);
  });
}

test("Home → Library → Series within the budget, CPU throttled ×6 (T04)", async ({
  page,
}, info) => {
  const log = record(page);
  const home = await mockHome(page);
  await start(page, "en");
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 6 });

  const t0 = Date.now();
  await page.goto("/tv/");
  await expect(page.getByTestId("big-hero")).toBeVisible({ timeout: 15_000 });
  const first = Date.now() - t0;

  await pressTo(page, "ArrowUp", /^tab:/);
  await pressTo(page, "ArrowRight", "tab:/big/libraries");
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("big-libraries")).toBeVisible();
  await pressTo(page, "ArrowDown", /^lib:/);
  await pressTo(page, "ArrowRight", "lib:L1");
  let t = Date.now();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-bs-focus="ltab:recommended"]')).toBeVisible();
  const toLibrary = Date.now() - t;

  // to a series by the Series tab, timed from OK on it to its seasons
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("big-libraries")).toBeVisible();
  await pressTo(page, "ArrowUp", /^tab:/);
  await pressTo(page, "ArrowRight", "tab:/big/series");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-bs-focus="series:S1"]')).toBeVisible();
  await pressTo(page, "ArrowDown", "series:S1");
  t = Date.now();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-bs-focus="season:1"]')).toBeVisible();
  const toSeries = Date.now() - t;

  const timing = { firstScreenMs: first, libraryMs: toLibrary, seriesMs: toSeries };
  await info.attach("timing.json", {
    body: JSON.stringify(timing, null, 2),
    contentType: "application/json",
  });
  await log.attach(info, home);
  // the budget: the first screen under 4 s, each next one under 3 s
  expect(first).toBeLessThan(4_000);
  expect(toLibrary).toBeLessThan(3_000);
  expect(toSeries).toBeLessThan(3_000);
});
