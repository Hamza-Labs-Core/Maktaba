// The server's status in its own window (Epic 13 story 13.13), against its references: the
// window as the shell opens it (960 × 640, dark, Windows' frame), with the server running (a
// release on its way, the transcription's install and the language model's download under way)
// and with a problem, in English and Arabic (mirrored as the mockup
// docs/design/desktop/mockups/server-status.html shows). The shell is stood in for by
// window.__MAKTABA_PANEL__, __MAKTABA_FRAME_PREVIEW__ (lib/frame.ts) and
// __MAKTABA_STATUS_PREVIEW__ (lib/statusWindow.ts): test data only, no media.
import { expect, test, type Page } from "@playwright/test";

type Lang = "en" | "ar";

const GB = 2 ** 30;

const RUNNING = {
  status: { kind: "running" },
  host: "Haddad-PC",
  version: "0.3.112",
  since: "2026-11-23T08:14:00Z",
  parts: [
    { id: "database", tone: "ok", running: 1 },
    { id: "server", tone: "ok", running: 1 },
    { id: "playback", tone: "ok", running: 2 },
    {
      id: "indexing",
      tone: "busy",
      running: 0,
      detail: "Installing (72%)",
      detail_code: "venv_building",
      detail_params: { percent: 72 },
      progress: 0.72,
    },
    {
      id: "model",
      tone: "busy",
      running: 1,
      model: "qwen2.5:7b",
      bytes: [3.2 * GB, 5 * GB],
      progress: 0.64,
    },
    { id: "plugins", tone: "off", running: 0 },
  ],
  update: { version: "0.3.113", by: "03:00", installing: false, install: false },
};

const PROBLEM = {
  ...RUNNING,
  status: { kind: "problem", roles: ["streaming"] },
  parts: [
    { id: "database", tone: "ok", running: 1 },
    { id: "server", tone: "ok", running: 1 },
    { id: "playback", tone: "error", running: 0 },
    { id: "indexing", tone: "ok", running: 1 },
    { id: "model", tone: "ok", running: 1, model: "qwen2.5:7b" },
    { id: "plugins", tone: "off", running: 0 },
  ],
  update: undefined,
};

test.use({ viewport: { width: 960, height: 640 }, timezoneId: "UTC" });

async function open(page: Page, lang: Lang, view: unknown) {
  await page.addInitScript(
    ({ lang, view }) => {
      localStorage.setItem("mkt:locale", lang);
      const w = window as unknown as Record<string, unknown>;
      w.__MAKTABA_PANEL__ = "status";
      w.__MAKTABA_FRAME_PREVIEW__ = { controls: "windows", side: "end", snapLayouts: true };
      w.__MAKTABA_STATUS_PREVIEW__ = view;
    },
    { lang, view }
  );
  await page.route("**/api/**", (route) =>
    route.fulfill({ status: 404, json: { error: "not_found" } })
  );
  await page.goto("/");
  await page.evaluate(() => document.fonts.ready);
  await page.mouse.move(480, 600);
}

for (const lang of ["en", "ar"] as const) {
  test.describe(`the status window (${lang})`, () => {
    test(`running (${lang})`, async ({ page }) => {
      await open(page, lang, RUNNING);
      await expect(page.getByTestId("status-window")).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");
      await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
      await expect(page.locator(".mkt-status__tile")).toHaveCount(6);
      await expect(page).toHaveScreenshot(`status-running-${lang}.png`);
    });

    test(`a problem (${lang})`, async ({ page }) => {
      await open(page, lang, PROBLEM);
      await expect(page.getByRole("alert")).toBeVisible();
      await expect(page).toHaveScreenshot(`status-problem-${lang}.png`);
    });
  });
}
