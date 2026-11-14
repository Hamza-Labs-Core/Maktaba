// Story 60.12 T08: the help index's filter. Typing narrows the list in the page, over each
// topic's title, summary and headings; the empty categories go, the count follows in the page's
// language, and no request leaves the page. Without JavaScript the box stays hidden and every
// topic is there. And a topic page: its table of contents goes to its sections, and an app route
// is text, not a link.
import { expect, test } from "@playwright/test";
import { settle } from "./pages";

for (const lang of ["en", "ar"] as const) {
  const pre = lang === "ar" ? "/ar" : "";
  const url = `${pre}/help/`;
  const list = "[data-filter-list]";

  test.describe(`${url} (${lang})`, () => {
    test("T08: help: typing narrows the list, with no request", async ({ page }) => {
      await page.goto(url);
      await settle(page);
      const items = page.locator(`${list} li`);
      const all = await items.count();
      expect(all, "the index lists every topic").toBe(46);
      await expect(page.locator("#count")).toContainText(String(all));
      const box = page.locator("#filter");
      await expect(box).toBeVisible();

      const requests: string[] = [];
      page.on("request", (r) => requests.push(r.url()));

      // a heading inside a topic, in no title or summary, finds that topic
      await box.fill(lang === "ar" ? "اختيار الوضع" : "Choose a mode");
      await expect(page.locator(`${list} li:visible`)).toHaveCount(1);
      await expect(page.locator(`${list} li:visible a`)).toHaveAttribute(
        "href",
        `${pre}/help/search/`
      );
      await expect(page.locator(`${list} section:visible`)).toHaveCount(1);
      await expect(page.locator("#count")).toHaveText(lang === "ar" ? "موضوع واحد" : "1 topic");

      // a word of the titles narrows to more than one
      await box.fill(lang === "ar" ? "البث" : "TV");
      const some = await page.locator(`${list} li:visible`).count();
      expect(some).toBeGreaterThan(1);
      expect(some).toBeLessThan(all);

      // nothing matches: the "no match" line; cleared: everything again
      await box.fill("zzzz-no-such-topic");
      await expect(page.locator("#no-match")).toBeVisible();
      await expect(page.locator(`${list} li:visible`)).toHaveCount(0);
      await box.fill("");
      await expect(page.locator(`${list} li:visible`)).toHaveCount(all);
      await expect(page.locator("#no-match")).toBeHidden();

      expect(requests, "the filter sends no request").toEqual([]);
    });

    test("help: a topic's contents go to its sections; an app route is text", async ({ page }) => {
      await page.goto(`${pre}/help/search/`);
      await settle(page);
      const toc = page.locator("nav.toc a");
      expect(await toc.count()).toBeGreaterThan(2);
      const first = (await toc.first().getAttribute("href"))!;
      await expect(page.locator(first)).toHaveCount(1);
      const where = page.locator(".where").first();
      await expect(where).toContainText(lang === "ar" ? "في مكتبة:" : "In Maktaba:");
      await expect(where.locator("a")).toHaveCount(0);
      await expect(page.locator("main a[href^='/settings'], main a[href^='/admin']")).toHaveCount(
        0
      );
      await expect(page.locator(".related .topics li")).toHaveCount(3);
    });
  });
}

test.describe("help without JavaScript", () => {
  test.use({ javaScriptEnabled: false });

  test("the box is hidden and every topic is there", async ({ page }) => {
    await page.goto("/help/");
    await expect(page.locator(".filter")).toBeHidden();
    await expect(page.locator("[data-filter-list] li:visible")).toHaveCount(46);
  });
});
