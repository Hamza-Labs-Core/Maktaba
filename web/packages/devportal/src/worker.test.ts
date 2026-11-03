// @vitest-environment node
import { describe, expect, it } from "vitest";
import { CSP_API, CSP_DEFAULT, cacheControl, movedTo } from "./headers";
import worker, { type Env } from "./worker";

// an assets binding over a few files: what wrangler's [assets] would serve from dist/
const FILES: Record<string, [string, string]> = {
  "/": ["<!doctype html>home", "text/html; charset=utf-8"],
  "/404": ["<!doctype html>not found page", "text/html; charset=utf-8"],
  "/plugins/": ["<!doctype html>plugins", "text/html; charset=utf-8"],
  "/api/": ["<!doctype html>api", "text/html; charset=utf-8"],
  "/assets/portal.css": ["body{}", "text/css"],
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

describe("redirects", () => {
  it("sends www to the apex, keeping path and query", async () => {
    const res = await get("https://www.maktaba.dev/plugins/?q=1");
    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("https://maktaba.dev/plugins/?q=1");
  });

  it("sends http to https", async () => {
    const res = await get("http://maktaba.dev/changelog/");
    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("https://maktaba.dev/changelog/");
  });

  it("redirects nothing in local dev (wrangler dev --var CANONICAL_REDIRECTS:off)", async () => {
    const env = { ...fakeEnv(), CANONICAL_REDIRECTS: "off" };
    expect((await get("http://maktaba.dev/", env)).status).toBe(200);
    expect((await get("http://www.maktaba.dev/", env)).status).toBe(200);
  });

  it("sends the old epics wiki to the specs on GitHub", async () => {
    for (const p of ["/wiki", "/wiki/", "/wiki/assets/index-Dy6jf78D.js", "/wiki/db/wiki.json"]) {
      const res = await get(`https://maktaba.dev${p}`);
      expect(res.status, p).toBe(301);
      expect(res.headers.get("Location"), p).toBe(
        "https://github.com/Hamza-Labs-Core/Maktaba/tree/main/specs/epics"
      );
    }
    expect(movedTo("/wikis")).toBeNull();
  });

  it("sends the old Epic 54 story pages to the plugin guides", async () => {
    const res = await get("https://maktaba.dev/plugins/story-54-01-manifest-package/");
    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("/plugins/");
    expect(movedTo("/plugins/manifest/")).toBeNull();
    expect((await get("https://maktaba.dev/plugins/")).status).toBe(200);
  });

  it("points /security.txt at /.well-known/security.txt", async () => {
    const res = await get("https://maktaba.dev/security.txt");
    expect(res.status).toBe(301);
    expect(res.headers.get("Location")).toBe("/.well-known/security.txt");
  });

  it("puts the security headers on a redirect too", async () => {
    const res = await get("https://www.maktaba.dev/");
    expect(res.headers.get("Strict-Transport-Security")).toMatch(/max-age=\d+/);
  });
});

describe("security headers", () => {
  it("every page gets HSTS, nosniff, a referrer policy and no framing", async () => {
    for (const path of ["/", "/plugins/", "/api/", "/nope"]) {
      const res = await get("https://maktaba.dev" + path);
      expect(res.headers.get("Strict-Transport-Security")).toBe("max-age=31536000");
      expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
      expect(res.headers.get("Referrer-Policy")).toBe("strict-origin-when-cross-origin");
      expect(res.headers.get("X-Frame-Options")).toBe("DENY");
      expect(res.headers.get("Content-Security-Policy")).toContain("frame-ancestors 'none'");
    }
  });

  it("the portal's own pages allow no inline script or style and no third party", async () => {
    const res = await get("https://maktaba.dev/");
    const csp = res.headers.get("Content-Security-Policy")!;
    expect(csp).toBe(CSP_DEFAULT);
    expect(csp).toContain("script-src 'self';");
    expect(csp).toContain("style-src 'self';");
    expect(csp).not.toContain("unsafe-inline");
    expect(csp).not.toMatch(/https:\/\//);
    expect(csp).toContain("object-src 'none'");
    expect(csp).toContain("base-uri 'none'");
  });

  it("the API reference may inline styles and use a blob: worker, but script stays 'self'", async () => {
    const csp = (await get("https://maktaba.dev/api/")).headers.get("Content-Security-Policy")!;
    expect(csp).toBe(CSP_API);
    expect(csp).toContain("script-src 'self';");
    expect(csp).toContain("worker-src 'self' blob:");
    expect(csp).not.toMatch(/https:\/\//);
  });

});

describe("caching", () => {
  it("HTML revalidates every time", async () => {
    const res = await get("https://maktaba.dev/");
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=0, must-revalidate");
  });

  it("other files cache for an hour", async () => {
    const res = await get("https://maktaba.dev/assets/portal.css");
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=3600");
  });

  it("errors are not cached", () => {
    expect(cacheControl("/x", "text/html", 404)).toBe("no-store");
  });
});

describe("the module", () => {
  it("exports only its handler (the Workers runtime rejects anything else)", async () => {
    const mod = await import("./worker");
    expect(Object.keys(mod)).toEqual(["default"]);
  });
});

describe("security.txt", () => {
  it("is served as text from /.well-known/", async () => {
    const res = await get("https://maktaba.dev/.well-known/security.txt");
    expect(res.status).toBe(200);
    expect(await res.text()).toContain("Contact: mailto:security@maktaba.dev");
    expect(res.headers.get("Cache-Control")).toBe("public, max-age=3600");
  });
});

describe("404", () => {
  it("a missing path gets the 404 page with a 404 status", async () => {
    const env = fakeEnv();
    const res = await get("https://maktaba.dev/no/such/page", env);
    expect(res.status).toBe(404);
    expect(await res.text()).toBe("<!doctype html>not found page");
    expect(res.headers.get("Content-Type")).toContain("text/html");
    expect(res.headers.get("Cache-Control")).toBe("no-store");
    expect(env.seen).toEqual(["/no/such/page", "/404"]);
  });

  it("still 404s in plain text when the 404 page itself is missing", async () => {
    const res = await get("https://maktaba.dev/x", fakeEnv({}));
    expect(res.status).toBe(404);
    expect(res.headers.get("Content-Type")).toContain("text/plain");
  });
});

describe("methods", () => {
  it("only GET and HEAD", async () => {
    const res = await get("https://maktaba.dev/", fakeEnv(), { method: "POST", body: "x" });
    expect(res.status).toBe(405);
    expect(res.headers.get("Allow")).toBe("GET, HEAD");
  });

  it("HEAD is served", async () => {
    const res = await get("https://maktaba.dev/", fakeEnv(), { method: "HEAD" });
    expect(res.status).toBe(200);
  });
});
