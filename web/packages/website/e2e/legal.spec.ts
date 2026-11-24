// Story 60.07 in a real browser: every legal page setting no cookie and reaching no other origin
// (T06), and the address of the copyright page the site no longer publishes opening the terms.
import { expect, test } from "@playwright/test";
import { settle } from "./pages";

const LEGAL = ["privacy", "terms"];

test.describe("the copyright page's old address opens the terms page", () => {
  for (const pre of ["", "/ar"])
    for (const from of [`${pre}/legal/copyright/`, `${pre}/legal/copyright`]) {
      test(from, async ({ page }) => {
        const res = await page.goto(from);
        await settle(page);
        expect(new URL(page.url()).pathname).toBe(`${pre}/legal/terms/`);
        expect(res?.status()).toBe(200);
        expect(res?.request().redirectedFrom()).not.toBeNull();
        await expect(page.locator('[data-legal="terms"]')).toHaveCount(1);
        // no link to the old page, and no copyright address, anywhere on it
        await expect(page.locator('a[href*="/legal/copyright"]')).toHaveCount(0);
        expect(await page.content()).not.toContain("copyright@");
      });
    }
});

test.describe("T06: the legal pages set no cookie and reach no other origin", () => {
  for (const name of LEGAL)
    for (const lang of ["en", "ar"]) {
      const url = `${lang === "ar" ? "/ar" : ""}/legal/${name}/`;
      test(url, async ({ page, context, baseURL }) => {
        const origin = new URL(baseURL!).origin;
        const seen: string[] = [];
        const cookies: string[] = [];
        page.on("request", (r) => seen.push(r.url()));
        page.on("response", async (r) => {
          const c = await r.headerValue("set-cookie");
          if (c) cookies.push(`${r.url()}: ${c}`);
        });
        await page.goto(url);
        await settle(page);
        // what the page does after load (a link hovered is not a request)
        await page.waitForLoadState("networkidle");
        const other = seen.filter((u) => !u.startsWith("data:") && new URL(u).origin !== origin);
        expect(other, `${url} reached another origin`).toEqual([]);
        expect(cookies).toEqual([]);
        expect(await context.cookies()).toEqual([]);
        expect(await page.evaluate(() => document.cookie)).toBe("");
      });
    }
});
