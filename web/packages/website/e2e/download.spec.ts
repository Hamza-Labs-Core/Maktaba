// Story 60.11 in a browser: T06 (the visitor's platform first, from the user agent or the
// address's #<platform>, with no request and no layout shift), each card's links, the "Verify
// this download" and "Other formats" disclosures by keyboard, and the verify panel's digest and
// command left to right on the Arabic page. The data are the built release's (dist/), whatever
// it is: the version on the page is the one in its <meta name="maktaba-release">.
import { expect, test, type Page } from "@playwright/test";
import { settle } from "./pages";

const UA = {
  mac: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
  windows:
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
  linux:
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
  iphone:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
  android:
    "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36",
} as const;

/** The cards' ids in document order and in the order they are drawn (top to bottom, then along
 * the line), and the platform the head script marked. */
async function order(page: Page) {
  return page.evaluate(() => {
    const cards = [...document.querySelectorAll<HTMLElement>(".cards > li")];
    const drawn = [...cards]
      .map((c) => ({ id: c.id, r: c.getBoundingClientRect() }))
      .sort(
        (a, b) =>
          a.r.top - b.r.top || (document.dir === "rtl" ? b.r.left - a.r.left : a.r.left - b.r.left)
      )
      .map((c) => c.id);
    return {
      dom: cards.map((c) => c.id),
      drawn,
      platform: document.documentElement.dataset.platform ?? "",
    };
  });
}

for (const lang of ["en", "ar"] as const) {
  const url = lang === "ar" ? "/ar/download/" : "/download/";

  test.describe(`${url} (${lang})`, () => {
    for (const [ua, want] of [
      ["mac", "mac"],
      ["windows", "windows"],
      ["linux", "linux"],
      ["iphone", ""],
      ["android", ""],
    ] as const) {
      test(`T06: a ${ua} user agent ${want ? `gets the ${want} card first` : "gets no preselection"}`, async ({
        browser,
      }) => {
        const ctx = await browser.newContext({ userAgent: UA[ua] });
        const page = await ctx.newPage();
        const requests: string[] = [];
        page.on("request", (r) => requests.push(r.url()));
        await page.goto(url);
        await settle(page);
        const o = await order(page);
        expect(o.platform).toBe(want);
        if (want) {
          expect(o.dom[0], "first in the document (keyboard, screen reader)").toBe(want);
          expect(o.drawn[0], "first on screen").toBe(want);
          await expect(page.locator(`#${want} .badge`)).toBeVisible();
          await expect(page.locator(`.platform-note [data-for="${want}"]`)).toBeVisible();
        } else {
          await expect(page.locator(".platform-note")).toBeHidden();
          await expect(page.locator(".cards .badge:visible")).toHaveCount(0);
        }
        // the platform is guessed in the page: no request beyond the page's own files
        const origin = new URL(page.url()).origin;
        expect(requests.filter((r) => !r.startsWith(origin))).toEqual([]);
        await ctx.close();
      });
    }

    test("T06: #<platform> in the address puts that card first, and the anchor lands on it", async ({
      browser,
    }) => {
      const ctx = await browser.newContext({ userAgent: UA.windows });
      const page = await ctx.newPage();
      await page.goto(`${url}#linux`);
      await settle(page);
      const o = await order(page);
      expect(o.platform).toBe("linux");
      expect(o.dom[0]).toBe("linux");
      await expect(page.locator("#linux")).toBeInViewport();
      // #nas is an anchor only: the user agent still picks the computer (a new page: a change
      // of the fragment alone loads nothing)
      const nas = await ctx.newPage();
      await nas.goto(`${url}#nas`);
      await settle(nas);
      expect((await order(nas)).platform).toBe("windows");
      await expect(nas.locator("#nas")).toBeInViewport();
      await ctx.close();
    });

    test("every card's links are the built release's files, and the page names its version", async ({
      page,
    }) => {
      await page.goto(url);
      const tag = await page.locator('meta[name="maktaba-release"]').getAttribute("content");
      expect(tag).toMatch(/^v\d+\.\d+\.\d+$/);
      const version = tag!.slice(1);
      await expect(page.locator(".page-head .meta")).toContainText(version);
      const files = await page
        .locator(".cards a[href^='https://github.com/']")
        .evaluateAll((as) => as.map((a) => (a as HTMLAnchorElement).href));
      expect(files.length).toBeGreaterThan(10);
      for (const f of files)
        expect(f).toMatch(
          new RegExp(
            `^https://github\\.com/Hamza-Labs-Core/Maktaba/releases/download/${tag}/[^/]+$`
          )
        );
      // the primary buttons carry the file's own name
      for (const b of await page.locator(".cards a.button[download]").all())
        expect(await b.getAttribute("href")).toContain(`/${await b.getAttribute("download")}`);
      await expect(page.locator(".all-files a")).toHaveAttribute(
        "href",
        `https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/${tag}`
      );
    });

    test("the disclosures open and close by keyboard, and the verify panel stays left to right", async ({
      page,
    }) => {
      await page.goto(url);
      await settle(page);
      const summaries = page.locator(".cards details.disclose > summary");
      const n = await summaries.count();
      expect(n).toBeGreaterThanOrEqual(6);
      for (let i = 0; i < n; i++) {
        const s = summaries.nth(i);
        const d = s.locator("..");
        await s.focus();
        await expect(s).toBeFocused();
        await page.keyboard.press("Enter");
        await expect(d).toHaveAttribute("open", "");
        await page.keyboard.press("Space");
        await expect(d).not.toHaveAttribute("open", "");
      }
      // open Mac's verify: the digest and the command read left to right in either language
      const verify = page.locator("#mac details.disclose").filter({ has: page.locator(".hash") });
      await verify.locator("summary").focus();
      await page.keyboard.press("Enter");
      const hash = verify.locator(".hash");
      await expect(hash).toBeVisible();
      await expect(hash).toHaveText(/^sha256:[0-9a-f]{64}$/);
      expect(await hash.evaluate((e) => getComputedStyle(e).direction)).toBe("ltr");
      expect(await verify.locator("pre").evaluate((e) => getComputedStyle(e).direction)).toBe(
        "ltr"
      );
      // its Copy button is reached by Tab from the summary
      await page.keyboard.press("Tab");
      await expect(verify.locator("button.copy")).toBeFocused();
    });
  });
}

test.describe("without JavaScript", () => {
  test.use({ javaScriptEnabled: false });

  test("every card shows, in the page's order, with no badge and no platform note", async ({
    page,
  }) => {
    await page.goto("/download/");
    const ids = await page.locator(".cards > li").evaluateAll((ls) => ls.map((l) => l.id));
    expect(ids.slice(0, 3)).toEqual(["mac", "windows", "linux"]);
    await expect(page.locator(".platform-note")).toBeHidden();
    await expect(page.locator(".cards .badge:visible")).toHaveCount(0);
  });
});
