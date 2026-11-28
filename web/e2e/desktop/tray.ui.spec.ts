// The tray's panel (Epic 13 story 13.12), against its references: the panel window as the
// shell opens it (360 × 560, dark), in English and Arabic, with the server running (an update
// ready, the language model downloading) and with a problem. The shell is stood in for by
// window.__MAKTABA_PANEL__ and window.__MAKTABA_TRAY_PREVIEW__ (lib/trayPanel.ts); the words
// are the tray's own (apps/desktop/src-tauri/src/lang.rs).
import { expect, test, type Page } from "@playwright/test";

type Entry = { id: string; label: string; enabled: boolean; separator: boolean };
const item = (id: string, label: string, enabled = true): Entry => ({
  id,
  label,
  enabled,
  separator: false,
});
const sep: Entry = { id: "", label: "", enabled: false, separator: true };

const WORDS = {
  en: {
    running: "Maktaba is running",
    problem: "Maktaba has a problem: the server does not answer",
    model: "Language model: downloading qwen2.5-7b (64%)",
    update: "Update ready: restart to install",
    open: "Open Maktaba",
    pause: "Pause server",
    logs: "Open logs",
    components: "Open Components",
    play: "Play",
    pausePlayback: "Pause playback",
    settings: "Settings",
    report: "Report a problem…",
    quit: "Quit (server keeps running)",
    stop: "Stop server and quit",
  },
  ar: {
    running: "مكتبة تعمل",
    problem: "في مكتبة مشكلة: الخادم لا يستجيب",
    model: "النموذج اللغوي: جارٍ تنزيل qwen2.5-7b (64%)",
    update: "التحديث جاهز: أعد التشغيل لتثبيته",
    open: "فتح مكتبة",
    pause: "إيقاف الخادم مؤقتًا",
    logs: "فتح السجلات",
    components: "فتح المكوّنات",
    play: "تشغيل",
    pausePlayback: "إيقاف التشغيل مؤقتًا",
    settings: "الإعدادات",
    report: "الإبلاغ عن مشكلة…",
    quit: "خروج (يبقى الخادم عاملًا)",
    stop: "إيقاف الخادم والخروج",
  },
};

function entries(lang: "en" | "ar", problem: boolean): Entry[] {
  const w = WORDS[lang];
  return [
    item("status", problem ? w.problem : w.running, false),
    ...(problem ? [] : [item("model-status", w.model, false), item("update", w.update)]),
    sep,
    item("show", w.open),
    item("server-pause", w.pause),
    item("open-logs", w.logs),
    item("open-components", w.components),
    sep,
    item("media-play", w.play),
    item("media-pause", w.pausePlayback),
    sep,
    item("tray-settings", w.settings),
    item("report-problem", w.report),
    sep,
    item("quit", w.quit),
    item("stop-and-quit", w.stop),
  ];
}

async function open(page: Page, lang: "en" | "ar", problem: boolean) {
  await page.addInitScript(
    ({ state }) => {
      const w = window as unknown as Record<string, unknown>;
      w.__MAKTABA_PANEL__ = "tray";
      w.__MAKTABA_TRAY_PREVIEW__ = state;
    },
    { state: { lang, tone: problem ? "error" : "ok", entries: entries(lang, problem) } }
  );
  await page.goto("/");
  await page.evaluate(() => document.fonts.ready);
  await expect(page.getByRole("status")).toBeVisible();
  // the pointer away from the rows; the focus ring on the first action is the panel's own
  await page.mouse.move(350, 2);
}

test.use({ viewport: { width: 360, height: 560 } });

for (const lang of ["en", "ar"] as const) {
  test(`the tray's panel, running (${lang})`, async ({ page }) => {
    await open(page, lang, false);
    await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");
    await expect(page).toHaveScreenshot(`tray-panel-running-${lang}.png`);
  });

  test(`the tray's panel, a problem (${lang})`, async ({ page }) => {
    await open(page, lang, true);
    await expect(page).toHaveScreenshot(`tray-panel-problem-${lang}.png`);
  });
}
