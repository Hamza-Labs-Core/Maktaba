// @vitest-environment node
// The build and the built site (dist/, from `npm run build`): the public-safety scan, the
// allow-list, the strings in both languages, security.txt, robots.txt and the sitemap.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { scanDir, scanText } from "../scripts/scan.mjs";
import {
  DOCS,
  MAX_EXPIRY_MONTHS,
  expiresAt,
  readDisclosurePolicy,
  rewriteHref,
  scrubMarkdown,
  markdownToHtml,
  securityTxt,
  slug,
} from "../scripts/build.mjs";
import { isSafeUrl } from "../scripts/urls.mjs";
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
    expect(home).toContain('href="https://github.com/Hamza-Labs-Core/Maktaba/tree/main/specs/epics"');
  });

  it("quotes no one: no owner's words on any page", () => {
    for (const p of files(DIST).filter((f) => f.endsWith(".html"))) {
      expect(read(p), p).not.toMatch(/the owner(?:'s|&#39;s)? (?:words|said|asked|decided)|The owner, 20\d\d/i);
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
    expect(rewriteHref("../security/conformance.md", from, published)).toBe("/security/conformance/");
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

  it("every page's strings are keys the toggle knows, with the toggle on the page", () => {
    const pages = files(DIST).filter((f) => f.endsWith(".html"));
    expect(pages.length).toBeGreaterThan(5);
    for (const p of pages) {
      const html = read(p);
      expect(html, p).toContain('id="lang-toggle"');
      expect(html, p).toMatch(/src="\/assets\/lang\.js\?v=[0-9a-f]{10}"/);
      for (const m of html.matchAll(/data-i18n(?:-label|-title)?="([^"]+)"/g)) {
        expect(STRINGS.en[m[1]], `${p}: ${m[1]}`).toBeDefined();
      }
    }
    expect(read("assets/i18n.js")).toContain("MAKTABA_STRINGS");
  });

  it("the portal's pages carry no inline script (the CSP allows none)", () => {
    // parsed as a browser would (parse5): every script has a src and no body, and no element
    // carries an event handler or a javascript: URL
    for (const p of files(DIST).filter((f) => f.endsWith(".html"))) {
      const els = elements(parse(read(p)));
      const scripts = els.filter((e) => e.tagName === "script");
      expect(scripts.length, p).toBeGreaterThan(0);
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
    for (const l of locs) expect(fs.existsSync(path.join(DIST, l, "index.html")), l).toBe(true);
  });

  it("there is a 404 page", () => {
    expect(read("404.html")).toContain('data-i18n="404.title"');
  });
});
