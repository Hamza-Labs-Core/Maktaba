// Story 60.01 part A's shell, as the built site serves it (scripts/shell.mjs): the language hint
// (Epic 60 README §6.5), the copy button with its spoken "Copied", the phone menu without script,
// and the 404 naming the address asked for. The keyboard, focus, target, reflow, motion and RTL
// checks of every page are the other specs'.
import fs from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { DIST, settle } from "./pages";

test.describe("the language hint (§6.5)", () => {
  test.describe("in a browser that prefers Arabic", () => {
    test.use({ locale: "ar-SA" });

    test("an English page offers its Arabic twin; Hide keeps it hidden, in localStorage only", async ({
      page,
      context,
    }) => {
      await page.goto("/install/");
      await settle(page);
      const hint = page.locator(".lang-hint");
      await expect(hint).toBeVisible();
      await expect(hint).toHaveAttribute("lang", "ar");
      await expect(hint).toHaveAttribute("dir", "rtl");
      await expect(hint.locator("a")).toHaveAttribute("href", "/ar/install/");
      await hint.locator("button").click();
      await expect(hint).toHaveCount(0);
      expect(await page.evaluate(() => localStorage.getItem("maktaba.langHint"))).toBe("dismissed");
      expect(await context.cookies()).toEqual([]);
      await page.goto("/");
      await settle(page);
      await expect(page.locator(".lang-hint")).toBeHidden();
    });

    test("an Arabic page shows none", async ({ page }) => {
      await page.goto("/ar/");
      await settle(page);
      await expect(page.locator(".lang-hint")).toHaveCount(0);
    });
  });

  test("an English page in an English browser shows none", async ({ page }) => {
    await page.goto("/");
    await settle(page);
    await expect(page.locator(".lang-hint")).toBeHidden();
  });
});

test.describe("the phone menu without script", () => {
  test.use({ javaScriptEnabled: false, viewport: { width: 360, height: 800 } });

  test("the nav stays a row of links, and no Menu button shows", async ({ page }) => {
    await page.goto("/");
    await expect(page.locator("#site-nav a").first()).toBeVisible();
    await expect(page.locator(".menu-button")).toBeHidden();
  });
});

test("the phone menu: the current page marked, Escape gives focus back", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await page.goto("/ar/problems/");
  await settle(page);
  const button = page.locator(".menu-button");
  await button.click();
  await expect(page.locator('#site-nav a[aria-current="page"]')).toHaveAttribute(
    "href",
    "/ar/problems/"
  );
  await page.locator("#site-nav a").first().focus();
  await page.keyboard.press("Escape");
  await expect(page.locator("#site-nav")).toBeHidden();
  await expect(button).toBeFocused();
});

test.describe("a code block's Copy button", () => {
  // no built page carries a shell code block yet (the guides keep their own): a fixture served
  // from the site's origin, with the shell's stylesheet and script as the site publishes them
  const shellFile = (prefix: string, ext: string) => {
    const f = fs
      .readdirSync(path.join(DIST, "assets"))
      .find((n) => n.startsWith(`${prefix}-`) && n.endsWith(ext));
    if (!f) throw new Error(`no assets/${prefix}-*${ext}: run npm run build`);
    return `/assets/${f}`;
  };

  for (const [lang, copy, done, name] of [
    ["en", "Copy", "Copied", "Copy the command"],
    ["ar", "نسخ", "تم النسخ", "نسخ الأمر"],
  ] as const) {
    test(`${lang}: copies the code, shows ${done} and says it, the code left to right`, async ({
      page,
      baseURL,
    }) => {
      const origin = new URL(baseURL!).origin;
      const dir = lang === "ar" ? "rtl" : "ltr";
      await page.route(`${origin}/__shell-fixture`, (r) =>
        r.fulfill({
          contentType: "text/html; charset=utf-8",
          body: `<!doctype html><html lang="${lang}" dir="${dir}"><head><title>x</title>
<link rel="stylesheet" href="${shellFile("site", ".css")}"><script src="${shellFile("shell", ".js")}" defer></script></head>
<body><main id="main"><div class="code"><div class="code-bar"><span class="name">x</span><button class="copy" type="button" aria-label="${name}" data-done="${done}"><span class="copy-label">${copy}</span></button></div>
<pre><code>docker compose up -d</code></pre></div></main><p class="visually-hidden" id="announce" aria-live="polite"></p></body></html>`,
        })
      );
      await page.addInitScript(() => {
        const w = window as unknown as { __copied: string[] };
        w.__copied = [];
        Object.defineProperty(navigator, "clipboard", {
          value: { writeText: async (t: string) => void w.__copied.push(t) },
        });
      });
      await page.goto("/__shell-fixture");
      await settle(page);
      const button = page.getByRole("button", { name });
      await expect(page.locator("pre")).toHaveCSS("direction", "ltr");
      await button.click();
      await expect(button.locator(".copy-label")).toHaveText(done);
      await expect(page.locator("#announce")).toHaveText(done);
      expect(
        await page.evaluate(() => (window as unknown as { __copied: string[] }).__copied)
      ).toEqual(["docker compose up -d"]);
      await expect(button.locator(".copy-label")).toHaveText(copy, { timeout: 4000 });
      await expect(page.locator("#announce")).toHaveText("");
    });
  }
});

test("the 404 names the address asked for, escaped", async ({ page }) => {
  const res = await page.goto("/instal/<b>synology/");
  expect(res?.status()).toBe(404);
  await expect(page.locator("code[data-asked]")).toHaveText("/instal/%3Cb%3Esynology/");
  await expect(page.locator(".lost .asked")).toBeVisible();
});
