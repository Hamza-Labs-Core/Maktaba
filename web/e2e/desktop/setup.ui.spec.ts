// The setup in its own window (Epic 13 story 13.13), against its references: the window as
// the shell opens it (960 × 640, dark, Windows' frame), the welcome and what will run, in
// English and Arabic (mirrored as the mockups docs/design/desktop/mockups/setup-welcome.html
// and setup-check.html show). The shell is stood in for by window.__MAKTABA_PANEL__,
// __MAKTABA_FRAME_PREVIEW__ (lib/frame.ts) and __MAKTABA_SETUP_PREVIEW__ (lib/desktopSetup.ts):
// test data only, no media (docs/design/desktop/README.md, "Demo media").
import { expect, test, type Page } from "@playwright/test";

type Lang = "en" | "ar";

const GB = 2 ** 30;
const CHECK = {
  ram_bytes: 16 * GB,
  disk_free_bytes: 412 * GB,
  data_dir: String.raw`C:\Users\haddad\AppData\Local\Maktaba\data`,
  need: { ram_bytes: 8 * GB, disk_bytes: 20 * GB },
  ok: true,
  problems: [],
  warnings: [{ code: "transcribe_cpu", message: "No NVIDIA graphics card" }],
  runs: [
    { role: "postgres", what: "The library database (private to Maktaba)" },
    { role: "api", what: "The server the app and your devices talk to" },
    { role: "streaming", what: "Playback and Live TV" },
    { role: "pipeline", what: "Indexing, transcription and search", params: { workers: 4 } },
  ],
};

test.use({ viewport: { width: 960, height: 640 } });

async function open(page: Page, lang: Lang) {
  await page.addInitScript(
    ({ lang, check }) => {
      localStorage.setItem("mkt:locale", lang);
      const w = window as unknown as Record<string, unknown>;
      w.__MAKTABA_PANEL__ = "setup";
      w.__MAKTABA_FRAME_PREVIEW__ = { controls: "windows", side: "end", snapLayouts: true };
      w.__MAKTABA_SETUP_PREVIEW__ = {
        state: {
          target: "setup",
          server_available: true,
          login_item: "not_registered",
          platform: "windows",
        },
        // not set up here (DesktopGate asks first), nothing to move in (Windows)
        steps: { status: { configured: false }, adopt: { sources: [] }, check },
      };
    },
    { lang, check: CHECK }
  );
  await page.route("**/api/**", (route) =>
    route.fulfill({ status: 404, json: { error: "not_found" } })
  );
  await page.goto("/");
  await page.evaluate(() => document.fonts.ready);
  // the pointer starts at (0, 0): over the close button of a right-to-left window
  await page.mouse.move(480, 600);
}

for (const lang of ["en", "ar"] as const) {
  test.describe(`the setup window (${lang})`, () => {
    test(`welcome (${lang})`, async ({ page }) => {
      await open(page, lang);
      await expect(page.getByTestId("setup-wizard")).toBeVisible();
      await expect(page.getByTestId("frame-bar")).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");
      await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
      await expect(page.getByRole("radio").first()).toBeChecked();
      await expect(page).toHaveScreenshot(`setup-welcome-${lang}.png`);
    });

    test(`what will run (${lang})`, async ({ page }) => {
      await open(page, lang);
      await page.getByRole("radio").first().focus();
      await page.locator(".mkt-setup__foot .mk-btn--primary").click();
      await expect(page.locator(".mkt-setup__runs li")).toHaveCount(4);
      await page.mouse.move(480, 600);
      await expect(page).toHaveScreenshot(`setup-check-${lang}.png`);
    });
  });
}
