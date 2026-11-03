// @vitest-environment node
// The build and the built site (dist/, from `npm run build`): the public-safety scan, the
// allow-list, every page in English and Arabic (and the checks that keep them paired),
// security.txt, robots.txt and the sitemap.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { scanDir, scanText } from "../scripts/scan.mjs";
import {
  DOCS,
  MAX_EXPIRY_MONTHS,
  REPO,
  checkSources,
  expiresAt,
  readDisclosurePolicy,
  rewriteHref,
  scrubMarkdown,
  markdownToHtml,
  securityTxt,
  slug,
} from "../scripts/build.mjs";
import { isSafeUrl } from "../scripts/urls.mjs";
import {
  MAX_LATIN_SHARE,
  applyOverlay,
  changelogOutline,
  checkChangelog,
  checkGuides,
  checkOverlay,
  checkPages,
  htmlProse,
  latinShare,
  openapiProse,
} from "../scripts/parity.mjs";
import { parse, type DefaultTreeAdapterMap } from "parse5";

type Element = DefaultTreeAdapterMap["element"];
type ParentNode = DefaultTreeAdapterMap["parentNode"];

/** Every element under a parsed document, in document order. */
function elements(node: ParentNode): Element[] {
  const out: Element[] = [];
  for (const child of node.childNodes) {
    if ("tagName" in child) out.push(child);
    if ("childNodes" in child) out.push(...elements(child as ParentNode));
  }
  if ("content" in node) out.push(...elements((node as DefaultTreeAdapterMap["template"]).content));
  return out;
}
const attr = (e: Element, name: string) => e.attrs.find((a) => a.name === name)?.value;
import { STRINGS as RAW } from "../scripts/strings.mjs";

const STRINGS = RAW as Record<"en" | "ar", Record<string, string>>;
const DIST = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../dist");
const read = (rel: string) => fs.readFileSync(path.join(DIST, rel), "utf8");

function files(dir: string, base = dir): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    return e.isDirectory() ? files(p, base) : [path.relative(base, p).split(path.sep).join("/")];
  });
}

describe("the public-safety scan", () => {
  it.each([
    ["a home path", "see /Users/someone/Code/Maktaba"],
    ["the Claude trail", "read docs/claude-trail.md first"],
    ["an audit", "docs/audits/2026-10-01.md"],
    ["a LAN address", "the NAS at 192.168.1.20"],
    ["a 10/8 address", "host 10.0.4.7"],
    ["a 172.16/12 address", "172.20.0.3"],
    ["a tailnet address", "100.101.102.103"],
    ["a personal mDNS name", "Someones-Mac-Studio.local"],
    ["a tailnet name", "studio.tail1234.ts.net"],
    ["a private key", "-----BEGIN OPENSSH PRIVATE KEY-----"],
    ["an AWS key", "AKIAIOSFODNN7EXAMPLE"],
    ["a GitHub token", "ghp_" + "a".repeat(36)],
    ["a Stripe live key", "sk_live_" + "a".repeat(24)],
    ["a Slack token", "xoxb-1234567890-abcdef"],
    [
      "a JWT",
      "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
    ],
    ["a token assignment", 'CLOUDFLARE_API_TOKEN="Zm9vYmFyYmF6cXV4cXV1eGNvcmdl"'],
    ["an account id", 'account_id = "0123456789abcdef0123456789abcdef"'],
  ])("flags %s", (_what, text) => {
    expect(scanText(text).length).toBeGreaterThan(0);
  });

  it.each([
    ["the product's mDNS name", "open maktaba.local in a browser"],
    ["the service type", "_maktaba._tcp.local"],
    ["a public address", "8.8.8.8 and 1.1.1.1"],
    ["a version number", "Go 1.24.10.2 and 10.15.7"],
    ["a placeholder", "[name].local"],
  ])("lets %s through", (_what, text) => {
    expect(scanText(text)).toEqual([]);
  });

  it("finds nothing in the built site", () => {
    expect(scanDir(DIST)).toEqual([]);
  });
});

describe("the allow-list", () => {
  it("publishes no internal document", () => {
    const all = files(DIST);
    expect(all.some((f) => /claude-trail|audits|advisories|\.md$/.test(f))).toBe(false);
  });

  it("publishes only the docs it names: the developer guides and the changelog", () => {
    const srcs = DOCS.map((d: { src: string }) => d.src);
    expect(srcs).toContain("CHANGELOG.md");
    expect(
      srcs.every((s: string) => s === "CHANGELOG.md" || s.startsWith("docs/developers/"))
    ).toBe(true);
    for (const d of DOCS) expect(fs.existsSync(path.join(DIST, d.out, "index.html"))).toBe(true);
  });

  // the epics and specs live on GitHub: no epics wiki, no spec or story page on this site
  it("carries no epics wiki and no spec pages", () => {
    const all = files(DIST);
    expect(all.some((f) => f.startsWith("wiki/"))).toBe(false);
    expect(all.some((f) => /story-\d\d-|specs\/epics/.test(f))).toBe(false);
    for (const p of all.filter((f) => f.endsWith(".html"))) {
      expect(read(p), p).not.toMatch(/href="\/wiki\//);
    }
  });

  it("has the plugin, contributing and security guides, each in its group's contents", () => {
    for (const p of [
      "plugins/index.html",
      "plugins/manifest/index.html",
      "plugins/sandbox/index.html",
      "plugins/queue-and-api/index.html",
      "plugins/testing-and-packaging/index.html",
      "plugins/publishing/index.html",
      "contributing/index.html",
      "contributing/architecture/index.html",
      "contributing/repo-layout/index.html",
      "contributing/local-setup/index.html",
      "contributing/tests/index.html",
      "contributing/conventions/index.html",
      "contributing/ci/index.html",
      "contributing/pull-requests/index.html",
      "security/conformance/index.html",
    ])
      expect(fs.existsSync(path.join(DIST, p)), p).toBe(true);
    const html = read("plugins/sandbox/index.html");
    expect(html).toContain('<nav class="toc"');
    expect(html).toContain('href="/plugins/manifest/"');
    expect(read("contributing/index.html")).toContain('href="/contributing/pull-requests/"');
    expect(read("security/policy/index.html")).toContain('href="/security/conformance/"');
    const home = read("index.html");
    for (const href of ["/plugins/", "/contributing/", "/api/", "/changelog/", "/security/policy/"])
      expect(home).toContain(`href="${href}"`);
    expect(home).toContain(
      'href="https://github.com/Hamza-Labs-Core/Maktaba/tree/main/specs/epics"'
    );
  });

  it("quotes no one: no owner's words on any page", () => {
    for (const p of files(DIST).filter((f) => f.endsWith(".html"))) {
      expect(read(p), p).not.toMatch(
        /the owner(?:'s|&#39;s)? (?:words|said|asked|decided)|The owner, 20\d\d/i
      );
    }
  });

  it("the API reference ships its OpenAPI description and Redoc from this site", () => {
    expect(JSON.parse(read("api/openapi.json")).openapi).toMatch(/^3\./);
    expect(fs.existsSync(path.join(DIST, "api/redoc.standalone.js"))).toBe(true);
    const html = read("api/index.html");
    expect(html).toMatch(/src="\/api\/redoc\.standalone\.js\?v=[0-9a-f]{10}"/);
    expect(html).not.toMatch(/<script[^>]+src="https?:/);
  });
});

describe("scrubbing markdown", () => {
  it("drops a bullet that mentions internal material, keeps the rest", () => {
    const md =
      "## Added\n\n- **One.** fine\n- **Two.** see `docs/claude-trail.md`\n  more of two\n- **Three.** fine\n";
    const out = scrubMarkdown(md);
    expect(out).toContain("**One.**");
    expect(out).toContain("**Three.**");
    expect(out).not.toContain("Two");
    expect(out).not.toContain("more of two");
  });

  it("replaces a machine's own mDNS name but keeps the product's", () => {
    const out = scrubMarkdown('renamed ("Someones-Mac-Studio-2.local"); open maktaba.local\n');
    expect(out).toContain('"[name].local"');
    expect(out).toContain("maktaba.local");
  });

  it("links a published doc locally and anything else to GitHub, never to internal files", () => {
    const published = new Map([
      ["docs/developers/plugins/README.md", "plugins/"],
      ["docs/developers/security/conformance.md", "security/conformance/"],
    ]);
    const from = "docs/developers/plugins/sandbox.md";
    expect(rewriteHref("README.md#the-plugin-model", from, published)).toBe(
      "/plugins/#the-plugin-model"
    );
    expect(rewriteHref("../security/conformance.md", from, published)).toBe(
      "/security/conformance/"
    );
    expect(rewriteHref("../../../specs/epics/54-plugins/README.md", from, published)).toBe(
      "https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/54-plugins/README.md"
    );
    expect(rewriteHref("../../claude-trail.md", from, published)).toBeNull();
    expect(rewriteHref("https://example.com/x", from, published)).toBe("https://example.com/x");
  });
});

describe("isSafeUrl", () => {
  it.each([
    "https://github.com/Hamza-Labs-Core/Maktaba",
    "http://example.com/",
    "mailto:security@maktaba.dev",
    "/contributing/",
    "../plugins/",
    "manifest/",
    "#4-the-plugin-model",
    "/api/openapi.json?v=1",
  ])("allows %s", (u) => {
    expect(isSafeUrl(u)).toBe(true);
  });

  it.each([
    "javascript:alert(1)",
    "JavaScript:alert(1)",
    "  javascript:alert(1)",
    "java\tscript:alert(1)",
    "java\nscript:alert(1)",
    "&#106;avascript:alert(1)",
    "&#x6A;avascript:alert(1)",
    "javascript&colon;alert(1)",
    "DATA:text/html,<script>alert(1)</script>",
    "data:image/svg+xml;base64,PHN2Zz4=",
    "vbscript:msgbox(1)",
    "VBScript:msgbox(1)",
    "file:///etc/passwd",
    "ftp://example.com/",
  ])("rejects %j", (u) => {
    expect(isSafeUrl(u)).toBe(false);
  });

  it("the renderer drops a link it rejects, keeping its text", () => {
    for (const href of ["DATA:text/html,x", "vbscript:x", "&#106;avascript:x"]) {
      const html = markdownToHtml(`[click](${href})`, "CHANGELOG.md", new Map());
      expect(
        elements(parse(html)).some((e) => e.tagName === "a"),
        href
      ).toBe(false);
      expect(html).toContain("click");
    }
  });
});

describe("heading ids", () => {
  it("are letters, digits and hyphens, from the heading's text", () => {
    expect(slug('5. The "no internet" guarantee')).toBe("5-the-no-internet-guarantee");
    expect(slug("`StageDef` and edges")).toBe("stagedef-and-edges");
    expect(slug('x" onmouseover="alert(1)')).toMatch(/^[\p{L}\p{N}-]+$/u);
  });
});

describe("English and Arabic", () => {
  it("every string has both languages", () => {
    const en = Object.keys(STRINGS.en).sort();
    const ar = Object.keys(STRINGS.ar).sort();
    expect(ar).toEqual(en);
    for (const k of en) {
      expect(STRINGS.en[k].trim(), k).not.toBe("");
      expect(STRINGS.ar[k].trim(), k).not.toBe("");
    }
  });

  it("the Arabic is Arabic", () => {
    for (const [k, v] of Object.entries(STRINGS.ar) as [string, string][]) {
      if (k === "lang.toggle") continue;
      expect(/[؀-ۿ]/.test(v), k).toBe(true);
    }
  });

  it("every page is in both languages, each linking the other", () => {
    const pages = files(DIST).filter((f) => f.endsWith(".html"));
    const en = pages.filter((f) => !f.startsWith("ar/"));
    const ar = pages.filter((f) => f.startsWith("ar/"));
    expect(en.length).toBeGreaterThan(15);
    expect(ar.map((f) => f.slice(3)).sort()).toEqual([...en].sort());
    for (const p of en) {
      const sitePath = "/" + p.replace(/index\.html$/, "").replace(/\.html$/, "");
      const html = read(p);
      expect(html, p).toContain('<html lang="en" dir="ltr">');
      expect(html, p).toMatch(new RegExp(`id="lang-switch"[^>]*href="/ar${sitePath}"`));
      const arHtml = read(`ar/${p}`);
      expect(arHtml, p).toContain('<html lang="ar" dir="rtl">');
      expect(arHtml, p).toMatch(new RegExp(`id="lang-switch"[^>]*href="${sitePath}"`));
      expect(arHtml, p).toContain(`hreflang="en" href="https://maktaba.dev${sitePath}"`);
    }
    // the Arabic chrome links Arabic pages
    const home = read("ar/index.html");
    for (const href of ["/ar/plugins/", "/ar/contributing/", "/ar/api/", "/ar/changelog/"])
      expect(home).toContain(`href="${href}"`);
    expect(read("ar/plugins/sandbox/index.html")).toContain('href="/ar/plugins/manifest/"');
  });

  it("an Arabic page is Arabic: its prose outside code is mostly Arabic script", () => {
    for (const p of files(DIST).filter((f) => f.startsWith("ar/") && f.endsWith(".html"))) {
      expect(latinShare(htmlProse(read(p))), p).toBeLessThan(MAX_LATIN_SHARE);
    }
    // and the check tells an English page apart
    expect(latinShare(htmlProse(read("plugins/manifest/index.html")))).toBeGreaterThan(0.9);
  });

  it("the Arabic API reference reads the description with its prose in Arabic", () => {
    const ar = JSON.parse(read("ar/api/openapi.json"));
    const en = JSON.parse(read("api/openapi.json"));
    expect(Object.keys(ar.paths)).toEqual(Object.keys(en.paths));
    expect(ar.info.title).toBe("واجهة مكتبة البرمجية");
    expect(ar.tags[0].name).toBe(en.tags[0].name);
    expect(/[؀-ۿ]/.test(ar.tags[0]["x-displayName"])).toBe(true);
    const html = read("ar/api/index.html");
    expect(html).toContain('data-spec="/ar/api/openapi.json"');
    expect(read("api/index.html")).toContain('data-spec="/api/openapi.json"');
  });

  it("the sources agree in both languages (the build's parity check)", () => {
    expect(checkSources()).toEqual([]);
  });

  it("a guide with no Arabic twin, or an Arabic guide with no English one, fails", () => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "parity-"));
    fs.mkdirSync(path.join(tmp, "g"));
    fs.writeFileSync(path.join(tmp, "g/a.md"), "# A");
    fs.writeFileSync(path.join(tmp, "g/b.ar.md"), "# ب");
    expect(checkGuides(tmp, "g", ["g/a.md"])).toEqual([
      "g/a.md: no Arabic version (g/a.ar.md)",
      "g/b.ar.md: an Arabic guide with no English page to pair",
    ]);
    fs.rmSync(tmp, { recursive: true });
  });

  it("an unpaired or English-only page fails the page check", () => {
    const english = "<p>This page was left in English, every word of it.</p>";
    const arabic = "<p>هذه الصفحة بالعربية، و`code` يبقى كما هو.</p>";
    expect(checkPages(["a.html", "ar/a.html"], () => arabic)).toEqual([]);
    expect(checkPages(["a.html", "ar/a.html", "b.html"], () => english)).toEqual([
      "ar/a.html: 100% of its prose is Latin script",
      "b.html: no Arabic page (ar/b.html)",
    ]);
    expect(checkPages(["ar/c.html"], () => arabic)).toEqual([
      "ar/c.html: an Arabic page with no English page",
    ]);
  });

  it("a changelog entry in one language only fails", () => {
    const en = fs.readFileSync(path.join(REPO, "CHANGELOG.md"), "utf8");
    const ar = fs.readFileSync(path.join(REPO, "CHANGELOG.ar.md"), "utf8");
    expect(checkChangelog(en, ar)).toEqual([]);
    expect(changelogOutline(ar, "ar")[0].release).toBe("Unreleased");
    const added = en.replace(
      "## [Unreleased]\n",
      "## [Unreleased]\n\n### Fixed\n\n- one more fix\n"
    );
    expect(checkChangelog(added, ar)).toHaveLength(1);
    expect(checkChangelog(added, ar)[0]).toMatch(/every entry is added in both languages/);
    // the same entry counts, with the sections named in Arabic
    const one = "## [Unreleased]\n\n### Fixed\n\n- a\n- b\n";
    expect(checkChangelog(one, "## [لم يصدر بعد]\n\n### أُصلح\n\n- أ\n- ب\n")).toEqual([]);
    expect(checkChangelog(one, "## [لم يصدر بعد]\n\n### أُصلح\n\n- أ\n")).toHaveLength(1);
  });

  it("the OpenAPI overlay translates every string, against the current English", () => {
    const doc = {
      info: { title: "T", description: "About." },
      tags: [{ name: "Auth" }],
      paths: { "/a/{id}": { get: { summary: "Get one." } } },
    };
    const prose = openapiProse(doc);
    expect(Object.keys(prose)).toEqual([
      "/info/title",
      "/info/description",
      "/tags/0/name",
      "/paths/~1a~1{id}/get/summary",
    ]);
    const strings = Object.fromEntries(
      Object.entries(prose).map(([k, en], i) => [k, { en, ar: `نص عربي ${i}` }])
    );
    expect(checkOverlay(doc, { strings })).toEqual([]);
    const out = applyOverlay(doc, { strings });
    expect(out.paths["/a/{id}"].get.summary).toBe("نص عربي 3");
    expect(out.tags[0]).toEqual({ name: "Auth", "x-displayName": "نص عربي 2" });
    // the English changed: translate again; a string left out, or left in English, fails
    const changed = { ...doc, info: { ...doc.info, description: "About, new." } };
    expect(checkOverlay(changed, { strings })[0]).toMatch(/the English changed/);
    const { ["/info/title"]: _dropped, ...rest } = strings;
    expect(checkOverlay(doc, { strings: rest })[0]).toMatch(/has no Arabic/);
    const english = { ...strings, "/info/description": { en: "About.", ar: "About." } };
    expect(checkOverlay(doc, { strings: english })[0]).toMatch(/not Arabic/);
  });

  it("the portal's pages carry no inline script (the CSP allows none)", () => {
    // parsed as a browser would (parse5): every script has a src and no body, and no element
    // carries an event handler or a javascript: URL
    for (const p of files(DIST).filter((f) => f.endsWith(".html"))) {
      const els = elements(parse(read(p)));
      const scripts = els.filter((e) => e.tagName === "script");
      for (const s of scripts) {
        expect(attr(s, "src"), p).toBeTruthy();
        expect(s.childNodes.length, p).toBe(0);
      }
      for (const e of els) {
        for (const a of e.attrs) {
          expect(a.name.startsWith("on"), `${p}: <${e.tagName} ${a.name}>`).toBe(false);
          if (a.name === "href" || a.name === "src") {
            // the renderer's own allow-list: http(s), mailto, or a path on this site
            expect(isSafeUrl(a.value), `${p}: ${a.value}`).toBe(true);
          }
        }
      }
    }
  });

  it("markdown's raw HTML is shown as text, never rendered", () => {
    const html = markdownToHtml(
      'Install <version> now.\n\n<script>alert(1)</script>\n\n<img src=x onerror="alert(1)">\n\n[x](javascript:alert(1))\n',
      "CHANGELOG.md",
      new Map()
    );
    const els = elements(parse(html));
    expect(els.some((e) => ["script", "img", "version"].includes(e.tagName))).toBe(false);
    expect(els.some((e) => e.attrs.some((a) => a.name.startsWith("on")))).toBe(false);
    expect(els.some((e) => e.tagName === "a")).toBe(false);
    expect(html).toContain("Install &lt;version&gt; now.");
  });
});

describe("security.txt, robots.txt, sitemap", () => {
  it("security.txt is the API's own disclosure policy, with an expiry ahead", () => {
    const policy = readDisclosurePolicy();
    const txt = read(".well-known/security.txt");
    const noExpires = (s: string) => s.replace(/^Expires: .*\n/m, "");
    expect(noExpires(txt)).toBe(noExpires(securityTxt(policy)));
    expect(txt).toMatch(/^Contact: mailto:security@maktaba\.dev$/m);
    expect(txt).toMatch(/^Policy: https:\/\/maktaba\.dev\/security\/policy$/m);
    expect(txt).toMatch(/^Canonical: https:\/\/maktaba\.dev\/\.well-known\/security\.txt$/m);
    expect(txt).toMatch(/^Preferred-Languages: en, ar$/m);
    // the rolling expiry: the build day (today, or yesterday for a build that ran past
    // midnight UTC) plus ExpiryMonths, ahead of now and under a year (RFC 9116)
    const m = txt.match(/^Expires: (\d{4}-\d\d-\d\dT00:00:00Z)$/m);
    expect(m).not.toBeNull();
    const exp = new Date(m![1]);
    const now = new Date();
    const day = 86400000;
    const built = [now, new Date(now.getTime() - day)].map((d) =>
      expiresAt(d, policy.expiryMonths).getTime()
    );
    expect(built).toContain(exp.getTime());
    expect(exp.getTime()).toBeGreaterThan(now.getTime());
    expect(exp.getTime()).toBeLessThan(now.getTime() + 365 * day);
  });

  it("the expiry rolls: ExpiryMonths after the build day, as the API's ExpiresAt", () => {
    const at = (iso: string) => readDisclosurePolicy(new Date(iso)).expires.toISOString();
    expect(readDisclosurePolicy().expiryMonths).toBe(MAX_EXPIRY_MONTHS);
    // the same cases as api/internal/security/security_test.go TestDefaultPolicyExpiryRolls
    expect(at("2026-11-03T15:04:05Z")).toBe("2027-10-03T00:00:00.000Z");
    expect(at("2027-01-01T00:00:00Z")).toBe("2027-12-01T00:00:00.000Z");
    expect(at("2026-03-31T12:00:00Z")).toBe("2027-03-03T00:00:00.000Z");
    expect(at("2026-11-03T23:30:00-05:00")).toBe("2027-10-04T00:00:00.000Z");
    // the file the build writes, for a build on a given day
    expect(securityTxt(readDisclosurePolicy(new Date("2026-11-03T09:00:00Z")))).toMatch(
      /^Expires: 2027-10-03T00:00:00Z$/m
    );
  });

  it("the policy and hall-of-fame pages security.txt names exist", () => {
    expect(fs.existsSync(path.join(DIST, "security/policy/index.html"))).toBe(true);
    expect(fs.existsSync(path.join(DIST, "security/hall-of-fame/index.html"))).toBe(true);
  });

  it("robots.txt allows all and names the sitemap", () => {
    expect(read("robots.txt")).toBe(
      "User-agent: *\nAllow: /\n\nSitemap: https://maktaba.dev/sitemap.xml\n"
    );
  });

  it("the sitemap lists every page, each one built", () => {
    const xml = read("sitemap.xml");
    const locs = [...xml.matchAll(/<loc>https:\/\/maktaba\.dev(\/[^<]*)<\/loc>/g)].map((m) => m[1]);
    expect(locs).toContain("/");
    expect(locs).not.toContain("/wiki/");
    expect(locs).toContain("/plugins/");
    expect(locs).toContain("/contributing/");
    expect(locs).toContain("/changelog/");
    expect(locs).toContain("/ar/");
    expect(locs).toContain("/ar/changelog/");
    for (const l of locs) expect(fs.existsSync(path.join(DIST, l, "index.html")), l).toBe(true);
  });

  it("there is a 404 page in each language", () => {
    expect(read("404.html")).toContain(`<h1>${STRINGS.en["404.title"]}</h1>`);
    expect(read("ar/404.html")).toContain(`<h1>${STRINGS.ar["404.title"]}</h1>`);
  });
});
