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
  WIKI_ALLOW,
  readDisclosurePolicy,
  rewriteHref,
  scrubMarkdown,
  securityTxt,
} from "../scripts/build.mjs";
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
    expect(all.some((f) => f.startsWith("wiki/mockups/"))).toBe(false);
  });

  it("every wiki file is one the allow-list names", () => {
    const wiki = files(DIST).filter((f) => f.startsWith("wiki/"));
    expect(wiki.length).toBeGreaterThan(3);
    for (const f of wiki) {
      const rel = f.slice("wiki/".length);
      expect(
        WIKI_ALLOW.some((re: RegExp) => re.test(rel)),
        f
      ).toBe(true);
    }
  });

  it("publishes only the docs it names", () => {
    const srcs = DOCS.map((d: { src: string }) => d.src);
    expect(srcs).toContain("CHANGELOG.md");
    expect(
      srcs.every((s: string) => s === "CHANGELOG.md" || s.startsWith("specs/epics/54-plugins/"))
    ).toBe(true);
    for (const d of DOCS) expect(fs.existsSync(path.join(DIST, d.out, "index.html"))).toBe(true);
  });

  it("the wiki loads from /wiki/", () => {
    const html = read("wiki/index.html");
    expect(html).toMatch(/src="\/wiki\/assets\/index-[^"]+\.js"/);
    for (const f of ["wiki.json", "wiki-cross-refs.json"]) {
      expect(fs.existsSync(path.join(DIST, "wiki/db", f))).toBe(true);
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
    const published = new Map([["specs/epics/54-plugins/README.md", "plugins/"]]);
    const from = "specs/epics/54-plugins/story-54-01-manifest-package.md";
    expect(rewriteHref("README.md#4-the-plugin-model", from, published)).toBe(
      "/plugins/#4-the-plugin-model"
    );
    expect(rewriteHref("../51-configurable-components/README.md", from, published)).toBe(
      "https://github.com/Hamza-Labs-Core/Maktaba/blob/main/specs/epics/51-configurable-components/README.md"
    );
    expect(rewriteHref("../../../docs/claude-trail.md", from, published)).toBeNull();
    expect(rewriteHref("https://example.com/x", from, published)).toBe("https://example.com/x");
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
    const pages = files(DIST).filter((f) => f.endsWith(".html") && !f.startsWith("wiki/"));
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
    for (const p of files(DIST).filter((f) => f.endsWith(".html"))) {
      for (const m of read(p).matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/g)) {
        expect(m[1], p).toMatch(/\bsrc=/);
        expect(m[2].trim(), p).toBe("");
      }
    }
  });
});

describe("security.txt, robots.txt, sitemap", () => {
  it("security.txt is the API's own disclosure policy, with an expiry ahead", () => {
    const policy = readDisclosurePolicy();
    const txt = read(".well-known/security.txt");
    expect(txt).toBe(securityTxt(policy));
    expect(txt).toMatch(/^Contact: mailto:security@maktaba\.dev$/m);
    expect(txt).toMatch(/^Expires: \d{4}-\d\d-\d\dT00:00:00Z$/m);
    expect(txt).toMatch(/^Policy: https:\/\/maktaba\.dev\/security\/policy$/m);
    expect(txt).toMatch(/^Canonical: https:\/\/maktaba\.dev\/\.well-known\/security\.txt$/m);
    expect(txt).toMatch(/^Preferred-Languages: en, ar$/m);
    expect(policy.expires.getTime()).toBeGreaterThan(Date.now());
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
    expect(locs).toContain("/wiki/");
    expect(locs).toContain("/plugins/");
    expect(locs).toContain("/changelog/");
    for (const l of locs) expect(fs.existsSync(path.join(DIST, l, "index.html")), l).toBe(true);
  });

  it("there is a 404 page", () => {
    expect(read("404.html")).toContain('data-i18n="404.title"');
  });
});
