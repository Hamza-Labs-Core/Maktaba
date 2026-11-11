// @vitest-environment node
// Story 60.03 T01–T04: the Worker's redirects, headers, 404 pages and caching, over a fake of
// the assets binding (what wrangler's [assets] serves from dist/).
import { describe, expect, it } from "vitest";
import { CSP_COPYRIGHT, CSP_DEFAULT, cacheControl, cspFor } from "./headers";
import worker, { type Env } from "./worker";

const HTML = "text/html; charset=utf-8";
const FILES: Record<string, [string, string]> = {
  "/": ['<!doctype html><html lang="en" dir="ltr">home', HTML],
  "/ar/": ['<!doctype html><html lang="ar" dir="rtl">الرئيسية', HTML],
  "/404": ['<!doctype html><html lang="en">This page isn\'t here', HTML],
  "/ar/404": ['<!doctype html><html lang="ar">هذه الصفحة غير موجودة', HTML],
  "/install/docker/": ['<!doctype html><main id="app"></main>', HTML],
  "/install/assets/docker-Bx3k9Q2a.js": ["export{}", "text/javascript"],
  "/assets/site.css": ["body{}", "text/css"],
  "/schema/log-line.json": [
    '{"$id":"https://maktaba.tv/schema/log-line.json"}',
    "application/json",
  ],
  "/.well-known/security.txt": ["Contact: mailto:security@maktaba.dev\n", "text/plain"],
};

function fakeEnv(files = FILES): Env & { seen: string[] } {
  const seen: string[] = [];
  return {
    seen,
    ASSETS: {
      fetch: async (req: Request) => {
        const path = new URL(req.url).pathname;
        seen.push(path);
        const f = files[path];
        if (!f) return new Response("asset missing", { status: 404 });
        return new Response(f[0], { headers: { "Content-Type": f[1] } });
      },
    },
  };
}

const get = (url: string, env: Env = fakeEnv(), init?: RequestInit) =>
  worker.fetch(new Request(url, init), env);

describe("redirects (T01, T04)", () => {
  it("T01: sends www to the apex, keeping path and query", async () => {
    const res = await get("https://www.maktaba.tv/install/docker/?from=app");
    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("https://maktaba.tv/install/docker/?from=app");
  });

  it("sends http to https", async () => {
    const res = await get("http://maktaba.tv/ar/");
    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("https://maktaba.tv/ar/");
  });

  it("redirects nothing canonical in local dev (CANONICAL_REDIRECTS off)", async () => {
    const env = { ...fakeEnv(), CANONICAL_REDIRECTS: "off" };
    expect((await get("http://maktaba.tv/", env)).status).toBe(200);
    expect((await get("http://www.maktaba.tv/", env)).status).toBe(200);
  });

  it("T04: /docs goes to the front door until the help pages exist, in each language", async () => {
    for (const [from, to] of [
      ["/docs", "/"],
      ["/docs/", "/"],
      ["/ar/docs", "/ar/"],
      ["/ar/docs/", "/ar/"],
    ]) {
      const res = await get(`https://maktaba.tv${from}`);
      expect(res.status, from).toBe(301);
      expect(res.headers.get("Location"), from).toBe(to);
    }
    // only /docs itself: a page under it is not a redirect
    expect((await get("https://maktaba.tv/docs/x")).status).toBe(404);
  });

  it("refuses anything but GET and HEAD", async () => {
    const res = await get("https://maktaba.tv/", fakeEnv(), { method: "POST" });
    expect(res.status).toBe(405);
    expect(res.headers.get("Allow")).toBe("GET, HEAD");
    expect(res.headers.get("Content-Security-Policy")).toBe(CSP_DEFAULT);
  });
});

describe("headers (T02)", () => {
  it("puts the §6.2 headers on every answer: pages, redirects, 404s", async () => {
    for (const url of [
      "https://maktaba.tv/",
      "https://maktaba.tv/ar/",
      "https://www.maktaba.tv/",
      "https://maktaba.tv/nope",
      "https://maktaba.tv/schema/log-line.json",
    ]) {
      const res = await get(url);
      const hsts = res.headers.get("Strict-Transport-Security") ?? "";
      expect(hsts, url).toMatch(/^max-age=\d{8}$/);
      expect(hsts, url).not.toMatch(/includeSubDomains/i);
      const csp = res.headers.get("Content-Security-Policy") ?? "";
      expect(csp, url).toContain("default-src 'self'");
      expect(csp, url).toContain("frame-ancestors 'none'");
      expect(res.headers.get("Referrer-Policy"), url).toBe("strict-origin-when-cross-origin");
      expect(res.headers.get("X-Content-Type-Options"), url).toBe("nosniff");
      expect(res.headers.get("Permissions-Policy"), url).toMatch(/camera=\(\).*microphone=\(\)/);
    }
  });

  it("names no other origin outside the copyright page", () => {
    expect(CSP_DEFAULT).not.toMatch(/https?:\/\//);
    expect(CSP_DEFAULT).not.toMatch(/unsafe-inline|unsafe-eval/);
    for (const p of ["/", "/ar/", "/install/docker/", "/legal/privacy/", "/legal/copyright/x"])
      expect(cspFor(p), p).toBe(CSP_DEFAULT);
  });

  it("lets the copyright page reach Turnstile and the cloud API, and nothing else", () => {
    for (const p of ["/legal/copyright/", "/legal/copyright", "/ar/legal/copyright/"])
      expect(cspFor(p), p).toBe(CSP_COPYRIGHT);
    const origins = new Set(CSP_COPYRIGHT.match(/https:\/\/[^\s;]+/g));
    expect([...origins].sort()).toEqual([
      "https://api.maktaba.tv",
      "https://challenges.cloudflare.com",
    ]);
    expect(CSP_COPYRIGHT).toContain("frame-ancestors 'none'");
  });
});

describe("404 (T03)", () => {
  it("answers a missing English path with the English 404 page and status 404", async () => {
    const env = fakeEnv();
    const res = await get("https://maktaba.tv/nope", env);
    expect(res.status).toBe(404);
    expect(await res.text()).toContain("This page isn't here");
    expect(env.seen).toEqual(["/nope", "/404"]);
    expect(res.headers.get("Cache-Control")).toBe("no-store");
  });

  it("answers a missing Arabic path with the Arabic 404 page", async () => {
    for (const p of ["/ar/nope", "/ar/install/nope/"]) {
      const res = await get(`https://maktaba.tv${p}`);
      expect(res.status, p).toBe(404);
      expect(await res.text(), p).toContain("هذه الصفحة غير موجودة");
    }
  });

  it("is case sensitive: /Install/Docker is not /install/docker", async () => {
    expect((await get("https://maktaba.tv/Install/Docker/")).status).toBe(404);
  });

  it("still answers 404 when the 404 page itself is missing", async () => {
    const res = await get("https://maktaba.tv/nope", fakeEnv({}));
    expect(res.status).toBe(404);
    expect(await res.text()).toBe("Not found\n");
  });
});

describe("caching", () => {
  it("keeps hashed assets for a year and revalidates HTML", async () => {
    const asset = await get("https://maktaba.tv/install/assets/docker-Bx3k9Q2a.js");
    expect(asset.headers.get("Cache-Control")).toBe("public, max-age=31536000, immutable");
    const page = await get("https://maktaba.tv/");
    expect(page.headers.get("Cache-Control")).toBe("public, max-age=0, must-revalidate");
    // an asset without a hash in its name is not immutable
    const css = await get("https://maktaba.tv/assets/site.css");
    expect(css.headers.get("Cache-Control")).toBe("public, max-age=3600");
  });

  it("matches only content-hashed names", () => {
    expect(cacheControl("/assets/fonts-a1b2c3d4.woff2", "font/woff2", 200)).toMatch(/immutable/);
    expect(cacheControl("/assets/site.css", "text/css", 200)).not.toMatch(/immutable/);
    expect(cacheControl("/install/docker/", HTML, 200)).toBe("public, max-age=0, must-revalidate");
    expect(cacheControl("/x", HTML, 404)).toBe("no-store");
  });
});
