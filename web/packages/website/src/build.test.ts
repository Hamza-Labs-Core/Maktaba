// @vitest-environment node
// Story 60.03 T05: the build's parts (the page sources, security.txt, the twin check), and the
// built dist/ itself. The second part reads dist/: run `npm run build` first (CI does).
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
// @ts-ignore: plain JavaScript build scripts
import * as b from "../scripts/build.mjs";
// @ts-ignore
import { page, twinOf } from "../scripts/shell.mjs";

const DIST: string = b.DIST;
const REPO: string = b.REPO;
const read = (rel: string) => fs.readFileSync(path.join(DIST, rel), "utf8");

describe("the build's parts", () => {
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
    expect(html).toContain('id="lang-switch" class="lang" href="/ar/x/"');
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
    expect(en).toContain('id="lang-switch" class="lang" href="/ar/"');
    expect(read("ar/index.html")).toContain('<html lang="ar" dir="rtl">');
    for (const f of ["404.html", "ar/404.html"]) expect(read(f), f).toContain('content="404"');
    expect(read("404.html")).toContain("This page isn't here");
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
        const rel = href.replace(/^\//, "");
        const file = rel === "" || rel.endsWith("/") ? `${rel}index.html` : rel;
        expect(fs.existsSync(path.join(DIST, file)), `${f}: ${href}`).toBe(true);
      }
    }
  });

  it("has robots.txt", () => {
    expect(read("robots.txt")).toBe("User-agent: *\nAllow: /\n");
  });
});
