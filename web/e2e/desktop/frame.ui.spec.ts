// The desktop app's own window frame (Epic 13 story 13.10), against its references: the
// app's header as the title bar on Windows (caption buttons), GNOME (header-bar buttons) and
// the Mac (the traffic lights' room); the slim bar of a page without the header (sign-in);
// the title bar's menu open. Dark, 1280×800, English and Arabic. The shell is stood in for by
// window.__MAKTABA_FRAME_PREVIEW__ (lib/frame.ts) and the home by page.route: test data only,
// no media (docs/design/desktop/README.md, "Demo media"). The references are the frame's
// strip (and the open menu's area), with the page under it hidden: other stories change it.
import { expect, test, type Page } from "@playwright/test";

type Lang = "en" | "ar";

const MENU = {
  en: [
    {
      title: "Maktaba",
      items: [
        { id: "preferences", label: "Settings…", enabled: true, separator: false },
        { id: "switch-server", label: "Switch Server…", enabled: true, separator: false },
      ],
    },
    {
      title: "File",
      items: [
        { id: "new-window", label: "New Window", enabled: true, separator: false },
        { id: "new-private", label: "New Private Window", enabled: true, separator: false },
      ],
    },
    {
      title: "View",
      items: [
        { id: "focus-search", label: "Find / Search…", enabled: true, separator: false },
        { id: "", label: "", enabled: false, separator: true },
        { id: "big-screen", label: "Big Screen", enabled: true, separator: false },
        {
          id: "big-screen-start",
          label: "Start in Big Screen",
          enabled: true,
          checked: true,
          separator: false,
        },
      ],
    },
    {
      title: "Help",
      items: [
        { id: "open-docs", label: "Documentation", enabled: true, separator: false },
        { id: "report-problem", label: "Report a Problem…", enabled: true, separator: false },
      ],
    },
    {
      title: "",
      items: [{ id: "frame-quit", label: "Quit Maktaba", enabled: true, separator: false }],
    },
  ],
  ar: [
    {
      title: "مكتبة",
      items: [
        { id: "preferences", label: "الإعدادات…", enabled: true, separator: false },
        { id: "switch-server", label: "تبديل الخادم…", enabled: true, separator: false },
      ],
    },
    {
      title: "ملف",
      items: [
        { id: "new-window", label: "نافذة جديدة", enabled: true, separator: false },
        { id: "new-private", label: "نافذة خاصة جديدة", enabled: true, separator: false },
      ],
    },
    {
      title: "عرض",
      items: [
        { id: "focus-search", label: "بحث…", enabled: true, separator: false },
        { id: "", label: "", enabled: false, separator: true },
        { id: "big-screen", label: "الشاشة الكبيرة", enabled: true, separator: false },
        {
          id: "big-screen-start",
          label: "البدء بالشاشة الكبيرة",
          enabled: true,
          checked: true,
          separator: false,
        },
      ],
    },
    {
      title: "مساعدة",
      items: [
        { id: "open-docs", label: "دليل الاستخدام", enabled: true, separator: false },
        { id: "report-problem", label: "الإبلاغ عن مشكلة…", enabled: true, separator: false },
      ],
    },
    {
      title: "",
      items: [{ id: "frame-quit", label: "إنهاء مكتبة", enabled: true, separator: false }],
    },
  ],
};

interface Setup {
  lang: Lang;
  controls: "windows" | "gtk" | "mac";
  signedIn: boolean;
  layout?: string;
  side?: "end" | "left" | "right";
}

async function open(page: Page, path: string, s: Setup) {
  await page.addInitScript(
    ({ lang, controls, layout, side, menu }) => {
      localStorage.setItem("mkt:locale", lang);
      localStorage.setItem("mkt:theme", "dark");
      (window as unknown as Record<string, unknown>).__MAKTABA_FRAME_PREVIEW__ = {
        controls,
        layout: layout ?? "",
        side: side ?? "end",
        snapLayouts: controls === "windows",
        menu,
      };
    },
    { lang: s.lang, controls: s.controls, layout: s.layout, side: s.side, menu: MENU[s.lang] }
  );
  await page.route("**/api/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/auth/me") {
      return s.signedIn
        ? route.fulfill({ json: { user: { id: "u1", username: "haddad", is_admin: false } } })
        : route.fulfill({ status: 401, json: { error: "unauthorized" } });
    }
    return route.fulfill({ status: 404, json: { error: "not_found" } });
  });
  await page.goto(path);
  await page.evaluate(() => document.fonts.ready);
  // the frame only: the page under it is other stories' (and changes with them)
  await page.addStyleTag({ content: ".mkt-shell__body { visibility: hidden !important; }" });
  // the pointer starts at (0, 0): over the close button of a right-to-left window
  await page.mouse.move(640, 700);
}

/** The title bar and a little of what is under it. */
const BAR = { clip: { x: 0, y: 0, width: 1280, height: 88 } };
/** The bar with its menu open. */
const MENU_AREA = { clip: { x: 0, y: 0, width: 1280, height: 600 } };

for (const lang of ["en", "ar"] as const) {
  test.describe(`the window's frame (${lang})`, () => {
    test(`sign-in: the slim title bar, Windows (${lang})`, async ({ page }) => {
      await open(page, "/login", { lang, controls: "windows", signedIn: false });
      await expect(page.getByTestId("frame-bar")).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");
      await expect(page).toHaveScreenshot(`frame-signin-windows-${lang}.png`, BAR);
    });

    for (const controls of ["windows", "gtk", "mac"] as const) {
      test(`the header as the title bar, ${controls} (${lang})`, async ({ page }) => {
        await open(page, "/help", {
          lang,
          controls,
          signedIn: true,
          layout: controls === "gtk" ? "appmenu:close" : "",
          side: controls === "mac" ? (lang === "ar" ? "right" : "left") : "end",
        });
        const header = page.getByRole("banner");
        await expect(header).toHaveAttribute("data-tauri-drag-region", "deep");
        await expect(page.getByTestId("frame-bar")).toHaveCount(0);
        await expect(page).toHaveScreenshot(`frame-header-${controls}-${lang}.png`, BAR);
      });
    }

    test(`the title bar's menu, open (${lang})`, async ({ page }) => {
      await open(page, "/help", { lang, controls: "windows", signedIn: true });
      await page.getByTestId("frame-menu").click();
      await expect(page.getByRole("menu")).toBeVisible();
      await expect(page).toHaveScreenshot(`frame-menu-windows-${lang}.png`, MENU_AREA);
    });
  });
}
