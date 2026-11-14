// cast.maktaba.tv's Worker: only /r/ is served, with the security and cache headers.

import { describe, expect, it } from "vitest";
import { cacheFor, SECURITY } from "../src/worker/headers";
import worker from "../src/worker/worker";

const assets = {
  async fetch(req: Request): Promise<Response> {
    const p = new URL(req.url).pathname;
    if (p === "/r/" || p === "/r/builds.json" || p === "/r/1/receiver.js")
      return new Response("ok", { status: 200 });
    return new Response("nope", { status: 404 });
  },
};

const get = (path: string, method = "GET") =>
  worker.fetch(new Request(`https://cast.maktaba.tv${path}`, { method }), { ASSETS: assets });

describe("the Worker", () => {
  it("serves the loader fresh, with the CSP", async () => {
    const res = await get("/r/");
    expect(res.status).toBe(200);
    expect(res.headers.get("Cache-Control")).toBe("no-cache");
    const csp = res.headers.get("Content-Security-Policy") ?? "";
    expect(csp).toContain("script-src 'self' https://www.gstatic.com");
    expect(csp).toContain("connect-src 'self' https:");
    expect(csp).not.toMatch(/connect-src[^;]*http:/);
    expect(res.headers.get("X-Content-Type-Options")).toBe("nosniff");
  });

  it("lets anyone read the manifest", async () => {
    const res = await get("/r/builds.json");
    expect(res.headers.get("Access-Control-Allow-Origin")).toBe("*");
  });

  it("sends the apex to maktaba.tv, /r to /r/, and 404s the rest", async () => {
    expect((await get("/")).headers.get("Location")).toBe("https://maktaba.tv/");
    expect((await get("/r")).headers.get("Location")).toBe("/r/");
    expect((await get("/admin")).status).toBe(404);
    expect((await get("/r/9/receiver.js")).status).toBe(404);
    expect((await get("/r/", "POST")).status).toBe(405);
  });

  it("caches builds briefly and the fonts for a day", () => {
    expect(cacheFor("/r/1/receiver.js")).toBe("public, max-age=300");
    expect(cacheFor("/r/fonts/maktaba-text-400-latin.woff2")).toBe("public, max-age=86400");
    expect(cacheFor("/r/loader.js")).toBe("no-cache");
    expect(SECURITY["Referrer-Policy"]).toBe("no-referrer");
  });
});
