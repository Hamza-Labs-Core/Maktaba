// Story 54.18 T03, T04, T05, T06 (the store's half): rating eligibility, an urgent report,
// plain-text reviews on the listing pages, English and Arabic, and the roles.
import { env } from "cloudflare:workers";
import { describe, expect, it } from "vitest";
import { countersign } from "../../web/publish";
import { type Dev, E, account, addPasskey, buildPackage, call, component, developer, grant, mailTo, submit } from "./helpers";

const writer = () => E.CATALOG_WRITER.get(E.CATALOG_WRITER.idFromName("main"));

async function listedPlugin(dev: Dev, name: string): Promise<string> {
  const id = `${dev.prefix}.${name}`;
  const sid = await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", dev.key, { capabilities: { optional: ["tags.write"] }, files: { "plugin.wasm": component("good") } }));
  await countersign(env as never, sid, "reviewed");
  await writer().regenerate();
  return id;
}

/** A buyer with a linked home that reported the plugin installed since `ago` ms. */
async function buyerWithHome(plugin: string, ago: number): Promise<{ cookie: string; id: string; home: string }> {
  const a = await account();
  const home = `h_${crypto.randomUUID().slice(0, 8)}`;
  await E.DB_INSTALLS.prepare("INSERT INTO homes (id, account, public_key, linked_at) VALUES (?, ?, 'x', ?)").bind(home, a.id, new Date().toISOString()).run();
  const since = new Date(Date.now() - ago).toISOString();
  await E.DB_INSTALLS.prepare("INSERT INTO installs (home, plugin, version, first_seen, last_seen) VALUES (?, ?, '1.0.0', ?, ?)").bind(home, plugin, since, since).run();
  return { cookie: a.cookie, id: a.id, home };
}

describe("ratings (T03)", () => {
  it("2 hours after install: too early; after 24 hours: rated; with no linked home: refused", async () => {
    const dev = await developer();
    const id = await listedPlugin(dev, "ratable");
    const early = await buyerWithHome(id, 2 * 3600_000);
    expect(await call("POST", `/v1/plugins/${id}/ratings`, { cookie: early.cookie, body: { stars: 4 } })).toMatchObject({ status: 403, body: { error: "rating_too_early" } });
    const later = await buyerWithHome(id, 25 * 3600_000);
    expect((await call("POST", `/v1/plugins/${id}/ratings`, { cookie: later.cookie, body: { stars: 4, text: "Good." } })).status).toBe(200);
    // one per account, editable
    expect((await call("POST", `/v1/plugins/${id}/ratings`, { cookie: later.cookie, body: { stars: 5, text: "Better." } })).status).toBe(200);
    const r = await call("GET", `/v1/plugins/${id}/ratings`, {});
    expect(r.body).toMatchObject({ count: 1, average: null });
    const nohome = await account();
    expect((await call("POST", `/v1/plugins/${id}/ratings`, { cookie: nohome.cookie, body: { stars: 3 } })).body.error).toBe("rating_no_home");
    expect((await call("POST", `/v1/plugins/${id}/ratings`, { body: { stars: 3 } })).status).toBe(401);
  });

  it("the developer replies once; a review can be reported and a moderator hides it", async () => {
    const dev = await developer();
    const id = await listedPlugin(dev, "replied");
    const b = await buyerWithHome(id, 30 * 3600_000);
    await call("POST", `/v1/plugins/${id}/ratings`, { cookie: b.cookie, body: { stars: 2, text: "Slow." } });
    const rid = (await E.DB_CATALOG.prepare("SELECT id FROM ratings WHERE plugin = ?").bind(id).first<{ id: string }>())!.id;
    expect((await call("POST", `/v1/ratings/${rid}/reply`, { cookie: b.cookie, body: { text: "not mine" } })).status).toBe(403);
    expect((await call("POST", `/v1/ratings/${rid}/reply`, { cookie: dev.cookie, body: { text: "Fixed in 1.1." } })).status).toBe(200);
    expect((await call("POST", `/v1/ratings/${rid}/reply`, { cookie: dev.cookie, body: { text: "again" } })).body.error).toBe("already_replied");
    expect((await call("POST", `/v1/ratings/${rid}/report`, { cookie: b.cookie })).status).toBe(200);
    const mod = await account();
    await grant(mod.id, "store_reviewer");
    const { cookie } = await addPasskey(mod.cookie);
    const q = await call("GET", "/v1/admin/reports", { cookie });
    expect((q.body.reviews as { id: string }[]).some((x) => x.id === rid)).toBe(true);
    expect((await call("POST", `/v1/admin/ratings/${rid}/hide`, { cookie })).status).toBe(200);
    expect((await call("GET", `/v1/plugins/${id}/ratings`, {})).body.count).toBe(0);
  });
});

describe("reports (T04)", () => {
  it("a malware report alerts the store admins and leads the moderation queue", async () => {
    const dev = await developer();
    const id = await listedPlugin(dev, "reported");
    const admin = await account();
    await grant(admin.id, "store_admin");
    const { cookie } = await addPasskey(admin.cookie);
    const reporter = await account();
    expect((await call("POST", `/v1/plugins/${id}/reports`, { cookie: reporter.cookie, body: { reason: "does_not_work" } })).status).toBe(201);
    expect((await call("POST", `/v1/plugins/${id}/reports`, { cookie: reporter.cookie, body: { reason: "malware", details: "it mines" } })).body.urgent).toBe(true);
    expect((await call("POST", `/v1/plugins/${id}/reports`, { cookie: reporter.cookie, body: { reason: "spam" } })).body.error).toBe("reason_invalid");
    expect(await mailTo(admin.email, "report_urgent")).not.toBeNull();
    const q = await call("GET", "/v1/admin/reports", { cookie });
    const mine = (q.body.reports as { plugin: string; reason: string; urgent: number }[]).filter((r) => r.plugin === id);
    expect(mine[0]).toMatchObject({ reason: "malware", urgent: 1 });
    const top = (q.body.reports as { urgent: number }[])[0]!;
    expect(top.urgent).toBe(1);
    const rid = (mine[0] as unknown as { id: string }).id;
    expect((await call("POST", `/v1/admin/reports/${rid}/resolve`, { cookie, body: { outcome: "disable" } })).status).toBe(200);
    expect(await mailTo(reporter.email, "report_resolved")).not.toBeNull();
  });
});

describe("the listing pages (T05, T08)", () => {
  it("a review with a URL and HTML is plain text; the Arabic page is right to left with the Arabic words", async () => {
    const dev = await developer();
    const id = await listedPlugin(dev, "listed");
    const b = await buyerWithHome(id, 30 * 3600_000);
    await call("POST", `/v1/plugins/${id}/ratings`, { cookie: b.cookie, body: { stars: 5, text: 'See https://evil.example <script>alert(1)</script> <a href="x">x</a>' } });
    const en = await (exports_fetch(`/p/${id}`));
    expect(en.status).toBe(200);
    const html = await en.text();
    expect(html).not.toContain("<script>alert");
    expect(html).not.toContain('<a href="x">');
    expect(html).not.toContain('href="https://evil.example');
    expect(html).toContain("&lt;script&gt;");
    expect(html).toContain("Open in my Maktaba");
    expect(html).toContain(`maktaba://plugins/${id}`);
    const ar = await (await exports_fetch(`/ar/p/${id}`)).text();
    expect(ar).toContain('dir="rtl"');
    expect(ar).toContain("الوسّام");
    expect(ar).toContain("افتح في مكتبتي");
    expect(ar).toContain("نزّل الملف");
    // Maktaba's own Arabic words for the capability
    expect(ar).toMatch(/الوسوم|وسوم/);
    const list = await (await exports_fetch("/")).text();
    expect(list).toContain(`/p/${id}`);
  });
});

async function exports_fetch(path: string): Promise<Response> {
  const { exports } = await import("cloudflare:workers");
  return (exports as unknown as { default: Fetcher }).default.fetch(`https://plugins.maktaba.tv${path}`);
}
