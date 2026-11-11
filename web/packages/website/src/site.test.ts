// @vitest-environment node
// Story 60.04 T10 and T11, and 60.08 T07: what every built page carries in its <head>, the
// social cards, the icons, sitemap.xml and robots.txt, and the link check's manifest equal to the
// build. Reads dist/: run `npm run build` first (CI does).
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
// @ts-ignore: plain JavaScript build scripts
import * as b from "../scripts/build.mjs";
// @ts-ignore
import { cardPath, woffToSfnt } from "../scripts/cards.mjs";
// @ts-ignore
import { icoOf } from "../scripts/icons.mjs";
// @ts-ignore
import { manifestOf } from "../scripts/links.mjs";

const DIST: string = b.DIST;
const PKG: string = b.PKG;
const read = (rel: string) => fs.readFileSync(path.join(DIST, rel), "utf8");

function walk(dir: string, base = dir, out: string[] = []): string[] {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, base, out);
    else out.push(path.relative(base, p).split(path.sep).join("/"));
  }
  return out;
}

interface Page {
  file: string;
  lang: "en" | "ar";
  own: string;
  path: string;
}
const pages = (fs.existsSync(DIST) ? b.sitePages(walk(DIST)) : []) as Page[];
const ORIGIN = "https://maktaba.tv";
const PNG_SIG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

/** The PNG's size and its chunk types. */
function png(buf: Buffer) {
  expect(buf.subarray(0, 8).equals(PNG_SIG)).toBe(true);
  const chunks: string[] = [];
  for (let at = 8; at < buf.length; ) {
    const len = buf.readUInt32BE(at);
    chunks.push(buf.toString("latin1", at + 4, at + 8));
    at += 12 + len;
  }
  return { width: buf.readUInt32BE(16), height: buf.readUInt32BE(20), chunks };
}

const attr = (html: string, re: RegExp) => html.match(re)?.[1];
const reEscape = (s: string) => s.replace(/[\\^$.*+?()[\]{}|/-]/g, "\\$&");
const meta = (html: string, key: string, name: string) =>
  attr(html, new RegExp(`<meta ${key}="${reEscape(name)}" content="([^"]*)"`));

describe("T10: every page's head", () => {
  it("has pages", () => expect(pages.length).toBeGreaterThan(10));

  for (const p of pages) {
    it(`${p.own}: title, description, canonical, alternates, Open Graph, Twitter, a card`, () => {
      const html = read(p.file);
      const here = `${ORIGIN}${p.own}`;
      const en = `${ORIGIN}${p.path}`;
      const ar = `${ORIGIN}/ar${p.path}`;
      expect(attr(html, /<title>([^<]+)<\/title>/)).toBeTruthy();
      expect(attr(html, /<meta\s+name="description"\s+content="([^"]+)"/)).toBeTruthy();
      // each once: a page with two canonicals or two og:titles is wrong
      expect(html.match(/rel="canonical"/g)).toHaveLength(1);
      expect(attr(html, /<link rel="canonical" href="([^"]+)"/)).toBe(here);
      for (const [lang, href] of [
        ["en", en],
        ["ar", ar],
        ["x-default", en],
      ]) {
        expect(html.match(new RegExp(`hreflang="${lang}" href=`, "g")), lang).toHaveLength(1);
        expect(
          attr(html, new RegExp(`<link rel="alternate" hreflang="${lang}" href="([^"]+)"`))
        ).toBe(href);
      }
      for (const k of ["og:title", "og:description", "og:image", "og:url", "og:locale"])
        expect(html.match(new RegExp(`property="${k}"`, "g")), k).toHaveLength(1);
      expect(meta(html, "property", "og:url")).toBe(here);
      expect(meta(html, "property", "og:locale")).toBe(p.lang === "ar" ? "ar_AR" : "en_US");
      expect(meta(html, "property", "og:locale:alternate")).toBe(
        p.lang === "ar" ? "en_US" : "ar_AR"
      );
      expect(meta(html, "name", "twitter:card")).toBe("summary_large_image");
      const image = meta(html, "property", "og:image");
      expect(image).toBe(`${ORIGIN}${cardPath(p.lang, p.path)}`);
      expect(meta(html, "name", "twitter:image")).toBe(image);
      // the card: 1200×630 PNG, under 200 KB, no metadata
      const card = fs.readFileSync(path.join(DIST, image!.slice(ORIGIN.length)));
      expect(card.length).toBeLessThan(200 * 1024);
      const info = png(card);
      expect([info.width, info.height]).toEqual([1200, 630]);
      expect(info.chunks.filter((c) => /^(tEXt|iTXt|zTXt|tIME|eXIf)$/.test(c))).toEqual([]);
      // the icons and the manifest
      for (const href of ["/favicon.ico", "/apple-touch-icon.png", "/manifest.webmanifest"])
        expect(html, href).toContain(`href="${href}"`);
      // the menu declaration the keyboard tests read
      expect(html).toMatch(/<meta name="maktaba-menu" content="(none|phone)" \/>/);
    });
  }

  it("the home page names the Organization (JSON-LD), in each language", () => {
    for (const [f, url] of [
      ["index.html", `${ORIGIN}/`],
      ["ar/index.html", `${ORIGIN}/ar/`],
    ]) {
      const m = read(f).match(/<script type="application\/ld\+json">([^<]+)<\/script>/);
      expect(m, f).not.toBeNull();
      const data = JSON.parse(m![1]);
      expect(data["@type"]).toBe("Organization");
      expect(data.url).toBe(url);
      expect(fs.existsSync(path.join(DIST, new URL(data.logo).pathname))).toBe(true);
    }
  });

  it("the 404 pages have the icons and no canonical (they have no address)", () => {
    for (const f of ["404.html", "ar/404.html"]) {
      expect(read(f)).toContain('href="/favicon.ico"');
      expect(read(f)).not.toContain('rel="canonical"');
    }
  });
});

describe("T11: sitemap.xml and robots.txt", () => {
  it("has one <url> per page, each with its en, ar and x-default alternates", () => {
    const xml = read("sitemap.xml");
    const urls = [...xml.matchAll(/<url>([\s\S]*?)<\/url>/g)].map((m) => m[1]);
    expect(urls.map((u) => u.match(/<loc>([^<]+)<\/loc>/)![1]).sort()).toEqual(
      pages.map((p) => `${ORIGIN}${p.own}`).sort()
    );
    for (const u of urls) {
      const loc = u.match(/<loc>([^<]+)<\/loc>/)![1];
      const p = pages.find((x) => `${ORIGIN}${x.own}` === loc)!;
      expect(u).toContain(`hreflang="en" href="${ORIGIN}${p.path}"`);
      expect(u).toContain(`hreflang="ar" href="${ORIGIN}/ar${p.path}"`);
      expect(u).toContain(`hreflang="x-default" href="${ORIGIN}${p.path}"`);
    }
  });

  it("robots.txt allows everything and names the sitemap", () => {
    expect(read("robots.txt")).toBe(
      "User-agent: *\nAllow: /\n\nSitemap: https://maktaba.tv/sitemap.xml\n"
    );
  });
});

describe("the icons and the manifest", () => {
  it("favicon.ico holds the 32 px app icon; the manifest's icons are served", () => {
    const ico = fs.readFileSync(path.join(DIST, "favicon.ico"));
    expect(ico.readUInt16LE(2)).toBe(1);
    expect(ico.readUInt8(6)).toBe(32);
    const m = JSON.parse(read("manifest.webmanifest"));
    expect(m.icons.map((i: { purpose: string }) => i.purpose)).toEqual(["any", "any", "maskable"]);
    for (const i of m.icons) expect(fs.existsSync(path.join(DIST, i.src)), i.src).toBe(true);
    expect(fs.existsSync(path.join(DIST, "apple-touch-icon.png"))).toBe(true);
  });

  it("icoOf refuses what is not a PNG; woffToSfnt refuses what is not WOFF 1.0", () => {
    expect(() => icoOf(Buffer.from("not a png at all, really"))).toThrow(/not a PNG/);
    expect(() => woffToSfnt(Buffer.from("wOF2xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"))).toThrow(
      /not a WOFF 1.0/
    );
  });
});

describe("60.08 T07: the link check's manifest", () => {
  it("dist-manifest.json is the build's (npm run build rewrites it: commit it)", () => {
    const committed = JSON.parse(fs.readFileSync(path.join(PKG, "dist-manifest.json"), "utf8"));
    expect(manifestOf(DIST, committed.redirects)).toEqual(committed);
  });
});
