// Maktaba's own dialog for the shell's errors (Epic 13 story 13.11), against its references:
// the 560 × 300 window, dark, in English and Arabic, with the programs' error and the way to
// the logs, and without them. The shell is stood in for by window.__MAKTABA_PANEL__ and
// window.__MAKTABA_DIALOG__ (lib/shellDialog.ts); the words are the shell's (lang.rs).
import { expect, test, type Page } from "@playwright/test";

const WORDS = {
  en: {
    brand: "Maktaba",
    close: "Close",
    seeLogs: "See the logs",
    server: "The Maktaba server could not start.",
    open: "Cannot open http://192.168.1.20:8080.",
  },
  ar: {
    brand: "مكتبة",
    close: "إغلاق",
    seeLogs: "عرض السجلات",
    server: "تعذّر تشغيل خادم مكتبة.",
    open: "يتعذّر فتح http://192.168.1.20:8080.",
  },
};

async function open(page: Page, lang: "en" | "ar", withError: boolean) {
  const w = WORDS[lang];
  const content = {
    lang,
    brand: w.brand,
    close: w.close,
    seeLogs: w.seeLogs,
    title: withError ? w.server : w.open,
    errors: withError
      ? [
          "maktaba-api: listen tcp 0.0.0.0:8080: bind: address already in use (held by: plexmediaserver.exe)",
        ]
      : [],
    logs: withError,
  };
  await page.addInitScript(
    ({ content }) => {
      const win = window as unknown as Record<string, unknown>;
      win.__MAKTABA_PANEL__ = "dialog";
      win.__MAKTABA_DIALOG__ = content;
      win.__MAKTABA_DIALOG_PREVIEW__ = true;
    },
    { content }
  );
  await page.goto("/");
  await page.evaluate(() => document.fonts.ready);
  await expect(page.getByRole("alertdialog")).toBeVisible();
  await page.mouse.move(280, 150);
}

test.use({ viewport: { width: 560, height: 300 } });

for (const lang of ["en", "ar"] as const) {
  test(`the server could not start, with its error and the logs (${lang})`, async ({ page }) => {
    await open(page, lang, true);
    await expect(page).toHaveScreenshot(`dialog-server-${lang}.png`);
  });

  test(`a page could not open, no logs here (${lang})`, async ({ page }) => {
    await open(page, lang, false);
    await expect(page).toHaveScreenshot(`dialog-open-${lang}.png`);
  });
}
