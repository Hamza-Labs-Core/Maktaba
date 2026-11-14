// T02 (story 58.07): the receiver page at 1920×1080, dark, in English and Arabic, with a stub of
// Google's SDK and a mock home: idle, loading, the overlay with Up next, paused, the errors and
// "Update the home"; mirrored in Arabic; the relay fallback within 6 seconds with the Remote
// badge. Screenshots are kept in test-results/ (and compared with e2e/__screenshots__ in the CI
// runner's image).

import { expect, test, type Page } from "@playwright/test";
import {
  arabicFontLoaded,
  expectedOrder,
  vectorInput,
  visualOrder,
} from "../../../../shared/i18n/visual_order.mjs";
import { customData, mockHome } from "./mockHome";

const visual = process.env.CAST_VISUAL === "1";

async function shot(page: Page, name: string) {
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: test.info().outputPath(`${name}.png`) });
  if (visual) await expect(page).toHaveScreenshot(`${name}.png`);
}

const screen = (page: Page) => page.locator(".mk");

for (const lang of ["en", "ar"] as const) {
  test.describe(`${lang}`, () => {
    // the device's language, for the idle screen before a sender names its own
    test.use({ locale: lang === "ar" ? "ar-EG" : "en-US" });
    test(`load, overlay, Up next, pause (${lang})`, async ({ page }, info) => {
      let release: () => void = () => {};
      const hold = new Promise<void>((r) => (release = r));
      await mockHome(page, info, { lang, holdSession: hold });
      await page.goto("/r/");
      await expect(screen(page)).toHaveAttribute("data-screen", "idle");
      await expect(page.locator(".mk-idle-text")).toHaveText(
        lang === "ar" ? "جاهز للتشغيل من Maktaba" : "Ready to play from Maktaba"
      );
      await shot(page, `idle-${lang}`);

      const loaded = page.evaluate(
        (d) =>
          (window as unknown as { __caf: { load(d: unknown): Promise<unknown> } }).__caf.load(d),
        customData(lang)
      );
      await expect(screen(page)).toHaveAttribute("data-screen", "loading");
      await expect(page.locator(".mk-loading-text")).toContainText(
        lang === "ar" ? "جارٍ تجهيز" : "Getting"
      );
      await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");
      await shot(page, `loading-${lang}`);
      release();
      const answer = (await loaded) as { media: Record<string, unknown> };
      expect(answer.media.hlsSegmentFormat).toBe("fmp4");
      expect(JSON.stringify(answer)).not.toContain("cast_");

      await page.evaluate(() => {
        const caf = (
          window as unknown as { __caf: { emit(t: string): void; at(t: number, d: number): void } }
        ).__caf;
        caf.emit("PLAYING");
        caf.at(5385, 5400);
      });
      await expect(screen(page)).toHaveAttribute("data-screen", "playing");
      await expect(page.locator(".mk-upnext")).toBeVisible();
      await expect(page.locator(".mk-pos")).toHaveText(
        lang === "ar" ? "‎1:29:45‎ من ‎1:30:00‎" : "‎1:29:45‎ of ‎1:30:00‎"
      );
      // mirrored: Up next sits at the end edge (the right in English, the left in Arabic)
      const box = await page.locator(".mk-upnext").boundingBox();
      expect(box).not.toBeNull();
      if (lang === "ar") expect(box!.x).toBeLessThan(960 - box!.width / 2);
      else expect(box!.x + box!.width).toBeGreaterThan(960 + box!.width / 2);
      await shot(page, `overlay-${lang}`);

      await page.evaluate(() =>
        (window as unknown as { __caf: { emit(t: string): void } }).__caf.emit("PAUSE")
      );
      await expect(screen(page)).toHaveAttribute("data-screen", "paused");
      await expect(page.locator(".mk-poster")).toBeVisible();
      const poster = await page.locator(".mk-poster").boundingBox();
      if (lang === "ar") expect(poster!.x).toBeGreaterThan(960);
      else expect(poster!.x).toBeLessThan(960);
      await shot(page, `paused-${lang}`);
    });

    test(`the relay within 6 seconds, with the Remote badge (${lang})`, async ({ page }, info) => {
      await mockHome(page, info, { lang, lanDown: true });
      await page.goto("/r/");
      const started = Date.now();
      await page.evaluate(
        (d) =>
          (window as unknown as { __caf: { load(d: unknown): Promise<unknown> } }).__caf.load(d),
        customData(lang)
      );
      await page.evaluate(() =>
        (window as unknown as { __caf: { emit(t: string): void } }).__caf.emit("PLAYING")
      );
      await expect(screen(page)).toHaveAttribute("data-screen", "playing");
      expect(Date.now() - started).toBeLessThan(6000);
      await expect(page.locator(".mk-badge")).toBeVisible();
      await expect(page.locator(".mk-badge")).toHaveText(
        lang === "ar" ? "عبر Maktaba Cloud" : "Through Maktaba Cloud"
      );
      const badge = await page.locator(".mk-badge").boundingBox();
      if (lang === "ar") expect(badge!.x).toBeLessThan(960);
      else expect(badge!.x).toBeGreaterThan(960);
    });

    test(`the home out of reach (${lang})`, async ({ page }, info) => {
      await mockHome(page, info, { lang, lanDown: true, relayDown: true });
      await page.goto("/r/");
      await page.evaluate(
        (d) =>
          (window as unknown as { __caf: { load(d: unknown): Promise<unknown> } }).__caf.load(d),
        customData(lang)
      );
      await expect(screen(page)).toHaveAttribute("data-screen", "error");
      await expect(page.locator(".mk-error-text")).toContainText(
        lang === "ar" ? "لا يستطيع التلفاز الوصول إلى منزلك" : "can't reach your home"
      );
      await shot(page, `error-home-${lang}`);
    });

    test(`a home older than every build (${lang})`, async ({ page }, info) => {
      await mockHome(page, info, {
        lang,
        builds: { builds: [{ level: 2, path: "/r/2/", built: "2026-11-01T00:00:00Z" }] },
      });
      await page.goto("/r/");
      const answer = await page.evaluate(
        (d) =>
          (window as unknown as { __caf: { load(d: unknown): Promise<unknown> } }).__caf.load(d),
        customData(lang)
      );
      expect(answer).toMatchObject({ type: "LOAD_FAILED", customData: { error: "update" } });
      await expect(screen(page)).toHaveAttribute("data-screen", "update");
      await expect(page.locator(".mk-error-text")).toContainText(
        lang === "ar" ? "حدِّث المنزل" : "Update the home"
      );
      await shot(page, `update-${lang}`);
    });
  });
}

// Story 58.15: "الحلقة 3 — Intro" reads Arabic first (on the right), then 3, the dash and Intro,
// as Roku's VisualText draws it and /tv/ shows it (vector episode.06), whatever the sender's
// language: a title is isolated and takes its direction from its first letter, in the bundled
// Arabic font.
for (const lang of ["en", "ar"] as const) {
  test(`an Arabic title reads in Roku's order, joined (${lang}, 58.15)`, async ({ page }, info) => {
    await mockHome(page, info, { lang, title: vectorInput("episode.06") });
    await page.goto("/r/");
    await expect(screen(page)).toHaveAttribute("data-screen", "idle");
    await page.evaluate(
      (d) => (window as unknown as { __caf: { load(d: unknown): Promise<unknown> } }).__caf.load(d),
      customData(lang)
    );
    await page.evaluate(() => {
      const caf = (
        window as unknown as { __caf: { emit(t: string): void; at(t: number, d: number): void } }
      ).__caf;
      caf.emit("PLAYING");
      caf.at(60, 5400);
    });
    await expect(screen(page)).toHaveAttribute("data-screen", "playing");
    const title = page.locator(".mk-title");
    await expect(title).toHaveText(vectorInput("episode.06"));
    await page.evaluate(() => document.fonts.ready);
    expect(await title.evaluate(visualOrder)).toBe(expectedOrder("episode.06"));
    // a face loads when text first needs it, which can be after document.fonts.ready
    await expect
      .poll(() => title.evaluate(arabicFontLoaded), { message: "the title's bundled Arabic font" })
      .toBe(true);
    await page.screenshot({ path: info.outputPath(`arabic-title-${lang}.png`) });
  });
}

test("the loader starts CAF with Shaka for HLS at the pinned version and the namespace", async ({
  page,
}, info) => {
  await mockHome(page, info, { lang: "en" });
  await page.goto("/r/");
  const started = await page.evaluate(
    () =>
      (window as unknown as { __caf: { state: { started: Record<string, unknown> } } }).__caf.state
        .started
  );
  expect(started).toMatchObject({
    useShakaForHls: true,
    shakaVersion: "4.15.12",
    customNamespaces: { "urn:x-cast:tv.maktaba.cast": "JSON" },
  });
});
