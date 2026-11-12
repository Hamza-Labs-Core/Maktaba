// Story 60.04, layout shift: the first paint waits for the whole document (scripts/shell.mjs's
// RENDER_WHOLE). A browser parses a page in pieces and may paint between two of them; a paint
// that fell inside the header drew the nav where the language switch belongs, and the nav slid
// over when the switch was parsed (e2e/cls.spec.ts failed on it about one run in three, on a
// different page each time). Here the race is forced: a server in the test hands the document
// over in two pieces, cut inside the header, and holds the second until the test lets it go.
// With the page as built nothing is painted while the second piece is held, and nothing shifts
// when it arrives; with the `expect` link taken out (the positive control) the first piece is
// painted and the nav shifts, so the test sees the race it guards against.
import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import type { AddressInfo } from "node:net";
import { expect, test, type Page } from "@playwright/test";
import { label, PKG, sitePages, type SitePage } from "./pages";

const EXPECT_LINK = /[ \t]*<link rel="expect" href="#announce" blocking="render" \/>\n/;

/** Where the document is cut: each is a place a paint fell in CI. */
const CUTS: Record<string, (html: string) => number> = {
  "after the nav, before the language switch": (html) => html.indexOf("</nav>") + "</nav>".length,
  "inside the language switch, before its label": (html) =>
    html.indexOf("<span>", html.indexOf('id="lang-switch"')),
};

const reps: { problems: Record<string, string> } = JSON.parse(
  fs.readFileSync(path.join(PKG, "e2e/representatives.json"), "utf8")
);
/** The 404 pages, the front doors and a problem page, in both languages. */
const PAGES = sitePages().filter(
  (p) =>
    p.name === "404" ||
    p.name === "home" ||
    p.url.replace(/^\/ar(?=\/)/, "") === reps.problems.shortest
);

interface Hold {
  /** resolves when the page's stylesheet has been answered: a paint is possible from here */
  styled: Promise<void>;
  /** sends the second piece */
  release: () => void;
}

let server: http.Server;
let origin = "";
const holds = new Map<string, Hold & { gate: Promise<void>; markStyled: () => void }>();

function hold(id: string): Hold {
  let release = () => {};
  let markStyled = () => {};
  const gate = new Promise<void>((r) => (release = r));
  const styled = new Promise<void>((r) => (markStyled = r));
  const h = { gate, styled, release, markStyled };
  holds.set(id, h);
  return h;
}

test.beforeAll(async ({}, info) => {
  const base = info.project.use.baseURL!;
  server = http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url!, base);
      const cut = url.searchParams.get("cut");
      // only the site under test is ever fetched: a path that is not one plain path of
      // it ("//host/..." names another host) is refused, and the request goes to the
      // site's own origin whatever was asked
      const site = new URL(base);
      const target = new URL(site.origin);
      if (!/^\/(?!\/)[\w\-./~%@+,:=]*$/.test(url.pathname) || url.origin !== site.origin) {
        res.writeHead(400).end();
        return;
      }
      target.pathname = url.pathname;
      const upstream = await fetch(target);
      const type = upstream.headers.get("content-type") ?? "application/octet-stream";
      if (!cut) {
        // everything a page loads (its stylesheet, scripts, fonts, images): the site's own
        res.writeHead(upstream.status, { "content-type": type });
        res.end(Buffer.from(await upstream.arrayBuffer()));
        if (type.startsWith("text/css")) for (const h of holds.values()) h.markStyled();
        return;
      }
      let html = await upstream.text();
      if (url.searchParams.get("control")) html = html.replace(EXPECT_LINK, "");
      const at = CUTS[cut](html);
      res.writeHead(upstream.status, { "content-type": "text/html; charset=utf-8" });
      res.write(html.slice(0, at));
      await holds.get(url.searchParams.get("hold")!)!.gate;
      res.end(html.slice(at));
    } catch (e) {
      res.destroy(e as Error);
    }
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
});

test.afterAll(async () => {
  for (const h of holds.values()) h.release();
  server.closeAllConnections();
  await new Promise((r) => server.close(r));
});

const OBSERVE = () => {
  const w = window as unknown as { __shifts: { value: number; sources: string[] }[] };
  w.__shifts = [];
  new PerformanceObserver((list) => {
    for (const e of list.getEntries() as unknown as {
      value: number;
      sources?: { node?: Node }[];
    }[]) {
      w.__shifts.push({
        value: e.value,
        sources: (e.sources ?? []).map((s) =>
          s.node instanceof Element ? `${s.node.tagName.toLowerCase()}.${s.node.className}` : "?"
        ),
      });
    }
  }).observe({ type: "layout-shift", buffered: true });
};

const painted = (page: Page) =>
  page.evaluate(() => performance.getEntriesByType("paint").length > 0);

/** Opens the page in two pieces; the second is held until the returned hold is released. */
async function openInTwo(page: Page, p: SitePage, cut: string, control = false): Promise<Hold> {
  const id = `${test.info().testId}-${test.info().repeatEachIndex}`;
  const h = hold(id);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(OBSERVE);
  const q = new URLSearchParams({ cut, hold: id, ...(control ? { control: "1" } : {}) });
  await page.goto(`${origin}${p.url}?${q}`, { waitUntil: "commit" });
  await h.styled;
  return h;
}

/** The shifts once the page is whole, loaded and painted (the paint is what could shift). */
async function settledShifts(page: Page) {
  await page.waitForLoadState("load");
  await page.waitForFunction(() => performance.getEntriesByType("paint").length > 0);
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise((r) => setTimeout(r, 300));
  });
  return page.evaluate(
    () => (window as unknown as { __shifts: { value: number; sources: string[] }[] }).__shifts
  );
}

test("every page asks the browser to wait for its last element, and has it", () => {
  for (const p of sitePages()) {
    const html = fs.readFileSync(path.join(PKG, "dist", p.file), "utf8");
    const head = html.slice(0, html.indexOf("</head>"));
    expect(head.match(EXPECT_LINK), `${p.file}: the expect link in its <head>`).not.toBeNull();
    expect(html.match(/\bid="announce"/g), `${p.file}: one #announce`).toHaveLength(1);
    expect(
      html.indexOf('id="announce"'),
      `${p.file}: #announce comes after the footer`
    ).toBeGreaterThan(html.indexOf("</footer>"));
  }
});

for (const p of PAGES) {
  test.describe(label(p), () => {
    for (const cut of Object.keys(CUTS)) {
      test(`cut ${cut}: nothing is painted until the rest arrives, and nothing shifts`, async ({
        page,
      }) => {
        const h = await openInTwo(page, p, cut);
        // the stylesheet is in: a browser that did not wait would paint the first piece now
        await page.waitForTimeout(1000);
        expect(await page.locator("#site-nav").count(), "the first piece is parsed").toBe(1);
        expect(await page.locator("#announce").count(), "the second piece is held").toBe(0);
        expect(await painted(page), "painted with half a header").toBe(false);
        h.release();
        expect(await settledShifts(page), "layout-shift entries").toEqual([]);
      });
    }
  });
}

for (const [p, cut] of [
  [PAGES.find((x) => x.name === "404" && x.lang === "en")!, Object.keys(CUTS)[0]],
  [PAGES.find((x) => x.name === "404" && x.lang === "ar")!, Object.keys(CUTS)[1]],
] as const) {
  test(`positive control, ${label(p)}, cut ${cut}: without the expect link the first piece is painted and the nav shifts`, async ({
    page,
  }) => {
    const h = await openInTwo(page, p, cut, true);
    await page.waitForFunction(() => performance.getEntriesByType("paint").length > 0);
    h.release();
    const found = await settledShifts(page);
    expect(found.flatMap((s) => s.sources)).toContain("nav.nav");
    expect(found.reduce((a, s) => a + s.value, 0)).toBeGreaterThan(0);
  });
}
