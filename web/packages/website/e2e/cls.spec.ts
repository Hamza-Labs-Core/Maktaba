// Story 60.04, layout shift: every page records its `layout-shift` entries through load and a
// scroll to the bottom, and has none; every image is sized (width and height). A fixture with
// an image that arrives unsized shows the observer catches a shift (the positive control).
import { expect, test, type Page } from "@playwright/test";
import { label, settle, sitePages } from "./pages";

const OBSERVE = () => {
  const w = window as unknown as { __shifts: { value: number; sources: string[] }[] };
  w.__shifts = [];
  new PerformanceObserver((list) => {
    for (const e of list.getEntries() as unknown as {
      value: number;
      hadRecentInput: boolean;
      sources?: { node?: Node }[];
    }[]) {
      if (e.hadRecentInput) continue;
      w.__shifts.push({
        value: e.value,
        sources: (e.sources ?? []).map((s) =>
          s.node instanceof Element ? `${s.node.tagName.toLowerCase()}.${s.node.className}` : "?"
        ),
      });
    }
  }).observe({ type: "layout-shift", buffered: true });
};

async function shifts(page: Page) {
  // scroll down in steps, as a reader would, then back up
  await page.evaluate(async () => {
    const step = innerHeight * 0.8;
    for (let y = 0; y < document.documentElement.scrollHeight; y += step) {
      scrollTo(0, y);
      await new Promise((r) => requestAnimationFrame(() => setTimeout(r, 50)));
    }
    scrollTo(0, 0);
    await new Promise((r) => setTimeout(r, 300));
  });
  return page.evaluate(
    () => (window as unknown as { __shifts: { value: number; sources: string[] }[] }).__shifts
  );
}

test("positive control: an image that arrives unsized shifts the layout, and the test sees it", async ({
  page,
}) => {
  // a fixture page at a real URL (about:blank reports no layout shift): an image with no
  // width or height arrives after the first paint and pushes the text below it down
  await page.route("**/__cls-fixture", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html lang="en"><body style="margin:0"><p>Above</p><div id="slot"></div>
        <p style="font-size:2rem">Below, pushed down when the image arrives</p></body></html>`,
    })
  );
  await page.addInitScript(OBSERVE);
  await page.goto("/__cls-fixture");
  await page.evaluate(async () => {
    await new Promise((r) => setTimeout(r, 300));
    const img = document.createElement("img");
    img.src =
      "data:image/svg+xml," +
      encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="300" height="200"/>');
    document.getElementById("slot")!.append(img);
    await img.decode();
    await new Promise((r) => setTimeout(r, 300));
  });
  const found = await shifts(page);
  expect(found.reduce((a, s) => a + s.value, 0)).toBeGreaterThan(0);
});

for (const p of sitePages()) {
  test.describe(label(p), () => {
    for (const width of [360, 1280]) {
      test(`no layout shift through load and scroll, at ${width} px`, async ({ page }) => {
        await page.setViewportSize({ width, height: 800 });
        await page.addInitScript(OBSERVE);
        await page.goto(p.url);
        await settle(page);
        const found = await shifts(page);
        expect(found, "layout-shift entries").toEqual([]);
      });
    }

    test("every image has its width and height", async ({ page }) => {
      await page.goto(p.url);
      await settle(page);
      const unsized = await page.$$eval("img", (imgs) =>
        imgs
          .filter((i) => !i.getAttribute("width") || !i.getAttribute("height"))
          .map((i) => i.getAttribute("src"))
      );
      expect(unsized).toEqual([]);
    });
  });
}
