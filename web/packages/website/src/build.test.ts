// @vitest-environment node
// Story 60.03 T05: the build's parts (the page sources, security.txt, the twin check), and the
// built dist/ itself. The second part reads dist/: run `npm run build` first (CI does).
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
// @ts-ignore: plain JavaScript build scripts
import * as b from "../scripts/build.mjs";
// @ts-ignore
import { page, twinOf } from "../scripts/shell.mjs";
// @ts-ignore
import { readPolicy, POLICY_FIELDS } from "../scripts/legal.mjs";

const DIST: string = b.DIST;
const REPO: string = b.REPO;
const read = (rel: string) => fs.readFileSync(path.join(DIST, rel), "utf8");

describe("the build's parts", () => {
  it("60.05 T07: drops a placeholder figure whose screenshot is missing, keeps it once it exists", () => {
    const fig = (shot: string) =>
      `      <figure class="placeholder" data-shot="${shot}">\n        <figcaption>📷 <strong>Screenshot to come:</strong> x</figcaption>\n      </figure>\n`;
    const html = `<li>\n      <p>Step one.</p>\n${fig("screenshots/qnap/a.en.png")}${fig("screenshots/qnap/b.en.png")}    </li>\n`;
    const out = b.dropMissingFigures(html, (s: string) => s === "screenshots/qnap/b.en.png");
    expect(out).toContain("<p>Step one.</p>");
    expect(out).not.toContain('data-shot="screenshots/qnap/a.en.png"');
    expect(out).toContain('data-shot="screenshots/qnap/b.en.png"');
    expect(b.dropMissingFigures(html, () => false)).toBe(
      "<li>\n      <p>Step one.</p>\n    </li>\n"
    );
    // the real lookup: a file the install site has, and one it does not
    expect(b.shotExists("install/install.css")).toBe(true);
    expect(b.shotExists("screenshots/truenas/no-such.en.png")).toBe(false);
    expect(b.shotExists("../../../LICENSE")).toBe(false);
  });

  it("60.06: resolves a built path and its fragment, and nothing outside dist/", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "resolver-"));
    try {
      fs.mkdirSync(path.join(dir, "install/troubleshooting"), { recursive: true });
      fs.writeFileSync(
        path.join(dir, "install/troubleshooting/index.html"),
        '<h2 id="port-in-use">x</h2>'
      );
      const r = b.resolver(dir);
      expect(r("/install/troubleshooting/")).toBe(true);
      expect(r("/install/troubleshooting")).toBe(true);
      expect(r("/install/troubleshooting/#port-in-use")).toBe(true);
      expect(r("/install/troubleshooting/#nope")).toBe(false);
      expect(r("/help/users/#seat-limit")).toBe(false);
      expect(r("/../../etc/passwd")).toBe(false);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  it("reads a page source's leading comment", () => {
    const p = b.parsePage("<!--\ntitle: T: x\ndescription: D\n-->\n<h1>x</h1>\n");
    expect(p).toEqual({ title: "T: x", description: "D", body: "<h1>x</h1>\n" });
    expect(() => b.parsePage("<h1>x</h1>", "x.html")).toThrow(/x.html: no leading/);
    expect(() => b.parsePage("<!--\ntitle: T\n-->\n", "y.html")).toThrow(/no description/);
  });

  it("renders security.txt with a rolling Expires and both Canonical lines", () => {
    const now = new Date("2026-03-31T15:00:00Z");
    const txt: string = b.securityTxt(b.readDisclosurePolicy(now));
    // 11 months after Mar 31 is Feb 31, which rolls to Mar 3 (as Go's time.Date)
    expect(txt).toContain("Expires: 2027-03-03T00:00:00Z\n");
    expect(txt).toContain("Canonical: https://maktaba.tv/.well-known/security.txt\n");
    expect(txt).toContain("Canonical: https://maktaba.dev/.well-known/security.txt\n");
    expect(txt).toMatch(/^Contact: mailto:/);
  });

  it("finds a page without its twin, in either direction", () => {
    expect(b.twinFindings(["index.html", "ar/index.html", "a.css"])).toEqual([]);
    expect(
      b.twinFindings(["index.html", "x/index.html", "ar/index.html", "ar/y/index.html"])
    ).toEqual([
      "ar/y/index.html: an Arabic page with no English twin",
      "x/index.html: no Arabic twin (ar/x/index.html)",
    ]);
  });

  it("links each page to its twin", () => {
    expect(twinOf("en", "/install/")).toBe("/ar/install/");
    expect(twinOf("ar", "/ar/install/")).toBe("/install/");
    expect(twinOf("ar", "/ar/")).toBe("/");
    const html: string = page({
      lang: "en",
      path: "/x/",
      title: "T",
      description: "D",
      body: "<p>b</p>",
    });
    expect(html).toContain('id="lang-switch" class="lang-switch" href="/ar/x/"');
    expect(html).toContain(
      '<link rel="alternate" hreflang="x-default" href="https://maktaba.tv/x/" />'
    );
  });
});

describe("the built site (dist/)", () => {
  it("has been built", () => {
    expect(fs.existsSync(path.join(DIST, "index.html")), "run `npm run build` first").toBe(true);
  });

  it("T05: serves the log schema at its $id, byte for byte", () => {
    const src = fs.readFileSync(path.join(REPO, "shared/log/schema/line.schema.json"));
    expect(fs.readFileSync(path.join(DIST, "schema/log-line.json")).equals(src)).toBe(true);
    expect(JSON.parse(src.toString()).$id).toBe("https://maktaba.tv/schema/log-line.json");
  });

  it("T05: security.txt has an Expires ahead and both Canonical lines", () => {
    const txt = read(".well-known/security.txt");
    const m = txt.match(/^Expires: (\S+)$/m);
    expect(m).not.toBeNull();
    expect(new Date(m![1]).getTime()).toBeGreaterThan(Date.now());
    expect(txt.match(/^Canonical: /gm)).toHaveLength(2);
  });

  it("has the front door and the 404 page in both languages", () => {
    const en = read("index.html");
    expect(en).toContain('<html lang="en" dir="ltr">');
    expect(en).toContain('id="lang-switch" class="lang-switch" href="/ar/"');
    expect(read("ar/index.html")).toContain('<html lang="ar" dir="rtl">');
    for (const f of ["404.html", "ar/404.html"]) expect(read(f), f).toContain('content="404"');
    expect(read("404.html")).toContain("This page isn’t here");
    expect(read("ar/404.html")).toContain("هذه الصفحة غير موجودة");
    for (const f of ["index.html", "ar/index.html", "404.html", "ar/404.html"])
      expect(read(f), f).toContain('<meta name="maktaba-site" content="maktaba-website" />');
  });

  it("has the install guides at the root, their hashed files under /install/assets/", () => {
    for (const g of ["nas", "docker", "synology", "truenas", "troubleshooting"]) {
      expect(fs.existsSync(path.join(DIST, `install/${g}/index.html`)), g).toBe(true);
      expect(fs.existsSync(path.join(DIST, `ar/install/${g}/index.html`)), g).toBe(true);
    }
    const docker = read("install/docker/index.html");
    expect(docker).toContain('<main id="app">');
    for (const src of docker.match(/(?:src|href)="\/[^"]+\.(?:js|css)"/g) ?? [])
      expect(src).toMatch(/="\/(install\/)?assets\//);
    expect(fs.existsSync(path.join(DIST, "install/assets"))).toBe(true);
  });

  it("60.05 T01: the nine guides and the chooser in both languages, the install assets", () => {
    const guides = [
      "nas",
      "docker",
      "synology",
      "truenas",
      "unraid",
      "qnap",
      "ugreen",
      "after-setup",
      "troubleshooting",
    ];
    const dirs = (rel: string) =>
      fs
        .readdirSync(path.join(DIST, rel), { withFileTypes: true })
        .filter((e) => e.isDirectory() && fs.existsSync(path.join(DIST, rel, e.name, "index.html")))
        .map((e) => e.name)
        .sort();
    expect(dirs("install")).toEqual([...guides].sort());
    expect(dirs("ar/install")).toEqual([...guides].sort());
    for (const f of ["install/index.html", "ar/install/index.html"]) {
      const html = read(f);
      const prefix = f.startsWith("ar/") ? "/ar" : "";
      for (const g of guides)
        expect(html, `${f}: ${g}`).toContain(`href="${prefix}/install/${g}/"`);
    }
    expect(fs.readdirSync(path.join(DIST, "install/assets")).some((f) => f.endsWith(".js"))).toBe(
      true
    );
  });

  it("60.05 T07: no guide shows a screenshot still to come", () => {
    for (const g of ["truenas", "unraid", "qnap", "ugreen"])
      for (const f of [`install/${g}/index.html`, `ar/install/${g}/index.html`]) {
        expect(read(f), f).not.toContain('class="placeholder"');
        expect(read(f), f).not.toContain("Screenshot to come");
      }
  });

  it("60.07: the legal pages in both languages, dated", () => {
    for (const n of ["privacy", "terms", "copyright"])
      for (const f of [`legal/${n}/index.html`, `ar/legal/${n}/index.html`]) {
        const html = read(f);
        expect(html, f).toMatch(/<time datetime="\d{4}-\d{2}-\d{2}">/);
        expect(html, f).toContain(`data-legal="${n}"`);
        expect(html, f).toContain('<meta name="maktaba-site" content="maktaba-website" />');
      }
  });

  it("60.07 T03 (the build): the copyright page gives the agent's email and the notice's contents, and no form or script but the shell's", () => {
    for (const f of ["legal/copyright/index.html", "ar/legal/copyright/index.html"]) {
      const html = read(f);
      expect(html, f).toContain('href="mailto:copyright@maktaba.tv"');
      expect(html, f).toMatch(
        /<section aria-labelledby="notice">[\s\S]*?<ol>\n(<li>[\s\S]*?<\/li>\n){7}<\/ol>/
      );
      expect(html, f).not.toContain("<form");
      // no script but the shell's own two (its head script and the menu's)
      const scripts = [...html.matchAll(/<script\b[^>]*>/gi)].map((m) => m[0]);
      expect(scripts, f).toHaveLength(2);
      for (const s of scripts)
        expect(s, f).toMatch(
          /^<script src="\/assets\/(head|shell)-[A-Za-z0-9_-]{10}\.js"( defer)?>$/
        );
      expect(html, f).not.toContain("challenges.cloudflare.com/turnstile/v0/api.js");
    }
    expect(
      fs.readdirSync(path.join(DIST, "assets")).some((f) => f.startsWith("copyright-form-"))
    ).toBe(false);
  });

  it("60.07 T01: the privacy page's cloud section equals `go run ./cmd/privacy-json`", () => {
    const policy = readPolicy(REPO);
    const html = read("legal/privacy/index.html");
    const unesc = (s: string) =>
      s
        .replace(/&lt;/g, "<")
        .replace(/&gt;/g, ">")
        .replace(/&quot;/g, '"')
        .replace(/&amp;/g, "&");
    const field = (f: string) => {
      const m = html.match(new RegExp(`<dd data-field="${f}">([\\s\\S]*?)</dd>`));
      expect(m, f).not.toBeNull();
      return m![1];
    };
    for (const f of POLICY_FIELDS as string[]) {
      const v = policy[f];
      const dd = field(f);
      if (Array.isArray(v))
        expect(
          [...dd.matchAll(/<li>([^<]*)<\/li>/g)].map((m) => unesc(m[1])),
          f
        ).toEqual(v);
      else if (f === "retention_days") expect(dd, f).toBe(`${v} days, then deleted`);
      else if (f === "contact") expect(dd, f).toBe(`<a href="mailto:${v}">${v}</a>`);
      else expect(unesc(dd), f).toBe(v);
    }
    // the page's date is never older than the policy's
    const date = html.match(/<time datetime="([\d-]+)">/)![1];
    expect(date >= policy.updated_at).toBe(true);
    // the Arabic page lists as many values as the policy has
    const ar = read("ar/legal/privacy/index.html");
    for (const f of ["data_categories", "not_collected", "rights"]) {
      const m = ar.match(new RegExp(`<dd data-field="${f}">([\\s\\S]*?)</dd>`))!;
      expect((m[1].match(/<li>/g) ?? []).length, f).toBe(policy[f].length);
    }
  });

  it("links only to pages it has, or to the latest release, GitHub and maktaba.dev", () => {
    const allowed = [
      "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest",
      "https://github.com/Hamza-Labs-Core/Maktaba",
      "https://maktaba.dev/",
      "https://maktaba.dev/ar/",
    ];
    for (const f of ["index.html", "ar/index.html", "404.html", "ar/404.html"]) {
      for (const [, href] of read(f).matchAll(/<a [^>]*href="([^"]+)"/g)) {
        if (/^https?:/.test(href)) {
          expect(allowed, `${f}: ${href}`).toContain(href);
          continue;
        }
        // an in-page link (the skip link's #main): links.mjs --dist checks fragments
        if (href.startsWith("#")) continue;
        const rel = href.replace(/^\//, "");
        const file = rel === "" || rel.endsWith("/") ? `${rel}index.html` : rel;
        expect(fs.existsSync(path.join(DIST, file)), `${f}: ${href}`).toBe(true);
      }
    }
  });

  it("has robots.txt (naming the sitemap, story 60.04)", () => {
    expect(read("robots.txt")).toMatch(/^User-agent: \*\nAllow: \/\n/);
  });
});
