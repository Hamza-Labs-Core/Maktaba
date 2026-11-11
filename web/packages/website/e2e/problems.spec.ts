// Story 60.06 T11: the problem index's filter. Typing narrows the list in the page, the empty
// areas go, the count follows in the page's language, and no request leaves the page; without
// JavaScript the box stays hidden and the whole list is there.
import { expect, test } from "@playwright/test";
import { settle } from "./pages";

for (const lang of ["en", "ar"] as const) {
  const url = lang === "ar" ? "/ar/problems/" : "/problems/";

  test.describe(`${url} (${lang})`, () => {
    test("T11: typing narrows the list, with no request", async ({ page }) => {
      await page.goto(url);
      await settle(page);
      const items = page.locator(".areas li");
      const all = await items.count();
      expect(all, "the index lists every entry").toBeGreaterThan(200);
      await expect(page.locator("#count")).toContainText(String(all));
      const box = page.locator("#filter");
      await expect(box).toBeVisible();

      const requests: string[] = [];
      page.on("request", (r) => requests.push(r.url()));

      // a slug narrows to its entry, whatever the language
      await box.fill("seat-limit");
      await expect(page.locator(".areas li:visible")).toHaveCount(1);
      await expect(page.locator(".areas li:visible a")).toHaveAttribute(
        "href",
        `${lang === "ar" ? "/ar" : ""}/problems/seat-limit/`
      );
      await expect(page.locator(".areas section:visible")).toHaveCount(1);
      await expect(page.locator("#count")).toHaveText(lang === "ar" ? "رسالة واحدة" : "1 message");

      // a word of the titles narrows to more than one
      await box.fill(lang === "ar" ? "الإضافة" : "plugin");
      const some = await page.locator(".areas li:visible").count();
      expect(some).toBeGreaterThan(1);
      expect(some).toBeLessThan(all);

      // nothing matches: the "no match" line; cleared: everything again
      await box.fill("zzzz-no-such-message");
      await expect(page.locator("#no-match")).toBeVisible();
      await expect(page.locator(".areas li:visible")).toHaveCount(0);
      await box.fill("");
      await expect(page.locator(".areas li:visible")).toHaveCount(all);
      await expect(page.locator("#no-match")).toBeHidden();

      expect(requests, "the filter sends no request").toEqual([]);
    });
  });
}

test.describe("without JavaScript", () => {
  test.use({ javaScriptEnabled: false });

  test("the box is hidden and the whole list is there", async ({ page }) => {
    await page.goto("/problems/");
    await expect(page.locator(".filter")).toBeHidden();
    expect(await page.locator(".areas li:visible").count()).toBeGreaterThan(200);
  });
});
