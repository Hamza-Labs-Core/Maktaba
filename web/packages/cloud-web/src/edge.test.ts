// @vitest-environment node
// Epic 50 story 50.32 T08: the two web Workers, each over a fake assets binding and a fake
// origin. For each host: an asset, an app route, a user API path, a console API path, the
// staff sign-in path; the host's headers on every answer, the origin reached only for the
// paths that host may pass, 404 for the rest.
import { describe, expect, it } from "vitest";
import { handle, type Env, type Site } from "./edge";
import { APP_CSP, APP_SITE } from "./site";
import { ADMIN_CSP, ADMIN_SITE } from "../../admin-web/src/site";

const FILES: Record<string, [string, string]> = {
  "/index.html": ["<!doctype html>app", "text/html; charset=utf-8"],
  "/assets/index-abc123.js": ["export{}", "text/javascript"],
};

function rig(site: Site) {
  const origin: string[] = [];
  const env: Env = {
    ASSETS: {
      fetch: async (req) => {
        const f = FILES[new URL(req.url).pathname];
        return f
          ? new Response(f[0], { headers: { "Content-Type": f[1] } })
          : new Response("missing", { status: 404 });
      },
    },
  };
  const serve = handle(site, async (req) => {
    origin.push(`${req.method} ${new URL(req.url).pathname}`);
    // the api's own answer, with a header the Worker must not drop
    return new Response('{"ok":true}', {
      headers: { "Content-Type": "application/json", "X-From-Origin": "1" },
    });
  });
  const req = (method: string, path: string) =>
    serve(new Request(`https://${site.host}${path}`, { method }), env);
  return { origin, req };
}

describe("app.maktaba.tv (maktaba-cloud-web)", () => {
  it("serves the bundle, and the SPA's index.html for an app route", async () => {
    const { req, origin } = rig(APP_SITE);
    const asset = await req("GET", "/assets/index-abc123.js");
    expect(asset.status).toBe(200);
    expect(asset.headers.get("Cache-Control")).toBe("public, max-age=31536000, immutable");
    const route = await req("GET", "/account/sessions");
    expect([route.status, await route.text()]).toEqual([200, "<!doctype html>app"]);
    expect(route.headers.get("Cache-Control")).toBe("no-cache");
    // a missing file is a 404, not the app
    expect((await req("GET", "/assets/gone.js")).status).toBe(404);
    expect(origin).toEqual([]);
  });

  it("passes the user API and /.well-known to the origin, unchanged", async () => {
    const { req, origin } = rig(APP_SITE);
    const res = await req("GET", "/v1/billing/me");
    expect(res.status).toBe(200);
    expect(res.headers.get("X-From-Origin")).toBe("1");
    await req("POST", "/v1/auth/login");
    await req("GET", "/.well-known/security.txt");
    expect(origin).toEqual([
      "GET /v1/billing/me",
      "POST /v1/auth/login",
      "GET /.well-known/security.txt",
    ]);
  });

  it("answers the console's API 404 itself, and never reaches the origin with it", async () => {
    const { req, origin } = rig(APP_SITE);
    for (const [m, p] of [
      ["GET", "/v1/admin/accounts"],
      ["POST", "/v1/admin/abuse/block"],
      ["POST", "/v1/staff/login"],
      ["GET", "/v1/admin"],
    ]) {
      const res = await req(m, p);
      expect([p, res.status]).toEqual([p, 404]);
      expect(await res.json()).toEqual({ error: "not_found", message: "Not found." });
    }
    expect(origin).toEqual([]);
  });

  it("puts the host's headers on every answer: asset, app route, API, 404", async () => {
    const { req } = rig(APP_SITE);
    for (const p of [
      "/assets/index-abc123.js",
      "/accounts",
      "/v1/billing/me",
      "/v1/admin/accounts",
    ]) {
      const h = (await req("GET", p)).headers;
      expect([p, h.get("Content-Security-Policy")]).toEqual([p, APP_CSP]);
      expect(h.get("Strict-Transport-Security")).toBe("max-age=31536000");
      expect(h.get("X-Content-Type-Options")).toBe("nosniff");
      expect(h.get("X-Frame-Options")).toBe("DENY");
      expect(h.get("X-Robots-Tag")).toBeNull();
    }
    expect(APP_CSP).toContain("script-src 'self' https://challenges.cloudflare.com");
    expect(APP_CSP).toContain("img-src 'self' data: https:");
  });

  it("refuses a change to a page", async () => {
    const { req } = rig(APP_SITE);
    expect((await req("POST", "/account")).status).toBe(405);
  });
});

describe("admin.maktaba.tv (maktaba-admin-web)", () => {
  it("passes the console's API and /healthz, nothing else", async () => {
    const { req, origin } = rig(ADMIN_SITE);
    const passed = [
      ["GET", "/v1/admin/accounts"],
      ["POST", "/v1/staff/login"],
      ["POST", "/v1/staff/refresh"],
      ["GET", "/healthz"],
    ];
    for (const [m, p] of passed) expect([p, (await req(m, p)).status]).toEqual([p, 200]);
    expect(origin).toEqual(passed.map(([m, p]) => `${m} ${p}`));
    origin.length = 0;
    for (const [m, p] of [
      ["GET", "/v1/billing/me"],
      ["POST", "/v1/auth/register"],
      ["GET", "/v1/account/sessions"],
      ["PATCH", "/v1/account/me"],
      ["GET", "/v1/auth/login"],
      // the customer sign-in the console used before staff accounts (50.33): no longer passed
      ["POST", "/v1/auth/login"],
      ["POST", "/v1/auth/refresh"],
      ["POST", "/v1/auth/logout"],
      ["GET", "/v1/account/me"],
      ["GET", "/readyz"],
    ]) {
      const res = await req(m, p);
      expect([m, p, res.status]).toEqual([m, p, p === "/readyz" ? 200 : 404]);
    }
    // /readyz is not an API path: the SPA answers it, the origin never sees it
    expect(origin).toEqual([]);
  });

  it("puts the console's headers on every answer, with no-store on HTML and /v1", async () => {
    const { req } = rig(ADMIN_SITE);
    for (const p of [
      "/assets/index-abc123.js",
      "/accounts",
      "/v1/admin/accounts",
      "/v1/billing/me",
    ]) {
      const h = (await req("GET", p)).headers;
      expect([p, h.get("Content-Security-Policy")]).toEqual([p, ADMIN_CSP]);
      expect(h.get("Strict-Transport-Security")).toBe("max-age=31536000");
      expect(h.get("X-Frame-Options")).toBe("DENY");
      expect(h.get("Referrer-Policy")).toBe("no-referrer");
      expect(h.get("Cross-Origin-Opener-Policy")).toBe("same-origin");
      expect(h.get("Cross-Origin-Resource-Policy")).toBe("same-origin");
      expect(h.get("X-Robots-Tag")).toBe("noindex, nofollow");
      expect(h.get("Permissions-Policy")).toContain("publickey-credentials-get=(self)");
      expect([p, h.get("Cache-Control")]).toEqual([
        p,
        p.startsWith("/assets/") ? "public, max-age=31536000, immutable" : "no-store",
      ]);
    }
    expect(ADMIN_CSP.startsWith("default-src 'none'")).toBe(true);
    expect(ADMIN_CSP).not.toContain("https:;");
  });

  it("disallows every robot", async () => {
    const { req } = rig(ADMIN_SITE);
    const res = await req("GET", "/robots.txt");
    expect(await res.text()).toBe("User-agent: *\nDisallow: /\n");
  });
});
