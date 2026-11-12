// Story 60.10 T03: the home page's Download button for a visitor on Windows, macOS, Linux, iOS
// and Android, and for one whose user agent hides the system. With JavaScript, src/home.ts names
// the system on <html> before the first paint (the button reads "Download for Mac" from its first
// frame) and gives each Download link to /download/ the system as its fragment; without it the
// button reads "Download Maktaba" and goes where the page says. Until story 60.11 builds
// /download/, Download goes to the latest release, with no fragment.
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { DIST, settle } from "./pages";

const AGENTS = {
  windows:
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
  mac: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
  linux: "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
  ios: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
  android:
    "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
};
const RELEASE = "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest";
const LABEL = {
  en: { any: "Download Maktaba", mac: "Download for Mac", windows: "Download for Windows" },
  ar: { any: "تنزيل البرنامج", mac: "التنزيل لجهاز Mac", windows: "التنزيل لنظام Windows" },
};
const hasDownload = fs.existsSync(path.join(DIST, "download/index.html"));

for (const lang of ["en", "ar"] as const) {
  const home = lang === "ar" ? "/ar/" : "/";
  const download = lang === "ar" ? "/ar/download/" : "/download/";

  for (const [platform, ua] of Object.entries(AGENTS)) {
    test.describe(`${home} on ${platform}`, () => {
      test.use({ userAgent: ua });

      test("T03: Download preselects the system", async ({ page }) => {
        await page.goto(home);
        await settle(page);
        await expect(page.locator("html")).toHaveAttribute("data-platform", platform);
        const button = page.locator(".hero a[data-download]");
        const named = platform === "mac" || platform === "windows" ? platform : "any";
        await expect(button).toHaveText(new RegExp(LABEL[lang][named]));
        for (const a of await page.locator("a[data-download]").all())
          await expect(a).toHaveAttribute(
            "href",
            hasDownload ? `${download}#${platform}` : RELEASE
          );
        if (hasDownload) {
          await button.click();
          await expect(page).toHaveURL(new RegExp(`${download}#${platform}$`));
        }
      });
    });
  }

  test.describe(`${home} with a user agent that hides the system`, () => {
    test.use({ userAgent: "Mozilla/5.0 (compatible; Maktaba-test)" });

    test("T03: Download goes to the download page with nothing chosen", async ({ page }) => {
      // the browser's client hints would name the test machine's system: hidden too
      await page.addInitScript(() => {
        Object.defineProperty(Navigator.prototype, "userAgentData", { get: () => undefined });
      });
      await page.goto(home);
      await settle(page);
      await expect(page.locator("html")).not.toHaveAttribute("data-platform", /.+/);
      await expect(page.locator(".hero a[data-download]")).toHaveText(new RegExp(LABEL[lang].any));
      for (const a of await page.locator("a[data-download]").all())
        await expect(a).toHaveAttribute("href", hasDownload ? download : RELEASE);
    });
  });

  test.describe(`${home} without JavaScript`, () => {
    test.use({ javaScriptEnabled: false, userAgent: AGENTS.mac });

    test("everything shows; only the preselection is missing", async ({ page }) => {
      await page.goto(home);
      await expect(page.locator(".hero a[data-download]")).toHaveText(new RegExp(LABEL[lang].any));
      for (const id of [
        "screens-title",
        "does-title",
        "arabic-title",
        "stays-title",
        "start-title",
      ])
        await expect(page.locator(`#${id}`)).toBeVisible();
      await expect(page.locator("main img")).toHaveCount(8);
    });
  });
}
