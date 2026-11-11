// The pages the browser tests visit: every page of the built dist/ in both languages, read from
// dist/ itself (so a new page is tested the day it is built), with the kind of each (hand-made,
// or the family that generates it) and the name its visual references take.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const PKG = path.resolve(HERE, "..");
export const DIST = path.join(PKG, "dist");

export type Lang = "en" | "ar";

export interface SitePage {
  /** what the browser opens: the page's own path, or a missing path for a 404 page */
  url: string;
  lang: Lang;
  /** "hand-made", or the family that generates it ("install", "problems", "help") */
  kind: string;
  /** the visual references' name: home, 404, install-nas, ... */
  name: string;
  file: string;
}

/** The family a page's English path belongs to, or "hand-made". */
export function kindOf(enPath: string): string {
  const m = enPath.match(/^\/(install|problems|help)\//);
  return m ? m[1] : "hand-made";
}

function walk(dir: string, base = dir, out: string[] = []): string[] {
  if (!fs.existsSync(dir)) return out;
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, base, out);
    else out.push(path.relative(base, p).split(path.sep).join("/"));
  }
  return out;
}

/** Every page, English first. The 404 pages are opened at a path the site does not have, so
 * the Worker answers them as it would a visitor (status 404). */
export function sitePages(dist = DIST): SitePage[] {
  const files = walk(dist).filter((f) => f.endsWith(".html"));
  if (!files.length) throw new Error(`no pages in ${dist}: run \`npm run build\` first`);
  const pages: SitePage[] = [];
  for (const file of files.sort()) {
    const lang: Lang = file.startsWith("ar/") ? "ar" : "en";
    const own = `/${file.replace(/index\.html$/, "")}`;
    const en = lang === "ar" ? own.slice(3) || "/" : own;
    if (/(^|\/)404\.html$/.test(file)) {
      pages.push({
        url: lang === "ar" ? "/ar/no-such-page-404" : "/no-such-page-404",
        lang,
        kind: "hand-made",
        name: "404",
        file,
      });
      continue;
    }
    const name = en === "/" ? "home" : en.replace(/^\/|\/$/g, "").replace(/\//g, "-");
    pages.push({ url: own, lang, kind: kindOf(en), name, file });
  }
  return pages.sort((a, b) => a.lang.localeCompare(b.lang) * -1 || a.url.localeCompare(b.url));
}

/** A test title for a page: its path and language. */
export const label = (p: SitePage) => `${p.url} (${p.lang})`;

/** Waits for what a page does on load: its scripts' rendering and its fonts. */
export async function settle(page: import("@playwright/test").Page): Promise<void> {
  await page.waitForLoadState("load");
  await page.evaluate(async () => {
    await document.fonts.ready;
  });
}
