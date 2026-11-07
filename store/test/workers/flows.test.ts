// Story 54.16 C12 at verification (T09, and the flow's route and decision): a flow is
// submitted as one upload of its members' packages, checked once all of them passed C1 to
// C11, with the shared flow checker (54.39) and the store's catalog, and refused, reviewed
// or approved as a whole. The packages are the shared corpus's own cases
// (shared/plugins/testdata/flows), their ids moved under the developer's prefix.
import { describe, expect, it } from "vitest";
import { runChecks } from "../../web/checks";
import { inspect, pack } from "../../web/wasm";
import { type Dev, E, account, addPasskey, buildPackage, call, component, developer, grant, submit } from "./helpers";

const enc = new TextEncoder();
type Case = { flow: string; plugins: Record<string, string> };
const corpus = (name: string) => (JSON.parse((E as unknown as { TEST_FLOWS: string }).TEST_FLOWS) as Record<string, Case>)[name]!;

/** A corpus case's flow and members for dev: ids under its prefix, packages that pass C1 to
 * C11 (the test component's export, an upper bound on maktaba, the payload schemas). */
function members(dev: Dev, name: string) {
  const c = corpus(name);
  const own = (s: string) => s.replaceAll("dev.example.", `${dev.prefix}.`);
  const flow = own(c.flow);
  return {
    flow,
    flowId: `${dev.prefix}.flow`,
    plugins: Object.entries(c.plugins).map(([id, toml]) => {
      const manifest = own(toml)
        .replace(/^maktaba = ">=0\.6\.0"$/m, 'maktaba = ">=0.6.0, <0.9.0"')
        .replace(/^export = "run-\d+"$/gm, 'export = "run-tag"')
        .replace(/^\[strings\]$/m, '[capabilities]\nrequired = []\noptional = ["tags.write"]\n\n[strings]');
      const payloads = [...manifest.matchAll(/^payload = "([^"]+)"$/gm)].map((m) => m[1]!);
      return { id: own(id), manifest, payloads };
    }),
  };
}

async function submitFlow(dev: Dev, name: string) {
  const m = members(dev, name);
  const fs = await call("POST", "/v1/flow-submissions", {
    cookie: dev.cookie,
    body: { flow_id: m.flowId, flow_version: "1.0.0", members: m.plugins.map((p) => p.id) },
  });
  expect(fs.status).toBe(201);
  const group = fs.body.id as string;
  const sids: string[] = [];
  for (const p of m.plugins) {
    const files: Record<string, Uint8Array> = { "plugin.wasm": component("good"), "flow.toml": enc.encode(m.flow) };
    for (const path of p.payloads) files[path] = enc.encode('{"type":"object","properties":{}}');
    const pkg = await buildPackage(p.id, "1.0.0", dev.key, { manifest: p.manifest, files });
    sids.push(await submit(dev, p.id, "1.0.0", pkg, { flow_submission: group }));
  }
  return { group, sids, ids: m.plugins.map((p) => p.id), flowId: m.flowId };
}

const row = (sid: string) =>
  E.DB_CATALOG.prepare("SELECT status, route, route_reasons, flow_state, report_key FROM submissions WHERE id = ?")
    .bind(sid)
    .first<{ status: string; route: string; route_reasons: string; flow_state: string; report_key: string }>();

describe("C12 at verification", () => {
  // T09: A's event starts B, B's starts C, C's starts A, submitted as one upload
  it("refuses a three-plugin flow with a cycle as a whole, naming the path in English and Arabic; nothing is countersigned", async () => {
    const dev = await developer();
    const f = await submitFlow(dev, "cycle_three_store");
    // the first two pass their own checks and wait for the third
    const first = await runChecks(E, f.sids[0]!);
    expect(first.checks.filter((c) => c.result === "block")).toEqual([]);
    await runChecks(E, f.sids[1]!);
    expect(await row(f.sids[0]!)).toMatchObject({ status: "checking", flow_state: "waiting" });
    const last = await runChecks(E, f.sids[2]!);
    const c12 = last.checks.find((c) => c.id === "C12")!;
    expect(c12).toMatchObject({ result: "block", code: "flow_cycle" });
    const [alpha, beta, gamma] = f.ids;
    expect(c12.path).toEqual([`${alpha}/one`, `plugin:${alpha}/a.done`, `${beta}/two`, `plugin:${beta}/b.done`, `${gamma}/three`, `plugin:${gamma}/c.done`, `${alpha}/one`]);
    expect(c12.detail).toContain(`${alpha}/one → plugin:${alpha}/a.done`);
    expect(c12.detail_ar).toContain("حلقة");
    for (const sid of f.sids) expect(await row(sid)).toMatchObject({ status: "rejected", route: "blocked", flow_state: "checked" });
    // nothing countersigned, nothing in the catalog
    for (const id of f.ids) {
      const v = await E.DB_CATALOG.prepare("SELECT status, store_sig FROM versions WHERE plugin = ?").bind(id).first<{ status: string; store_sig: string | null }>();
      expect(v).toMatchObject({ status: "rejected" });
      expect(v!.store_sig ?? "").toBe("");
    }
    const fs = await call("GET", `/v1/flow-submissions/${f.group}`, { cookie: dev.cookie });
    expect(fs.body.flow_submission).toMatchObject({ status: "rejected" });
    expect((fs.body.report as { findings: { code: string }[] }).findings.map((x) => x.code)).toContain("flow_cycle");
  });

  it("a member refused at its own checks refuses the flow: the others are not countersigned", async () => {
    const dev = await developer();
    const f = await submitFlow(dev, "chain_two_store");
    await runChecks(E, f.sids[0]!);
    // the second member's package goes missing: C1 blocks it
    const key = (await E.DB_CATALOG.prepare("SELECT package_key FROM submissions WHERE id = ?").bind(f.sids[1]).first<{ package_key: string }>())!.package_key;
    await E.PRIVATE.delete(key);
    await runChecks(E, f.sids[1]!);
    expect(await row(f.sids[0]!)).toMatchObject({ status: "rejected" });
    const obj = await E.PRIVATE.get((await row(f.sids[0]!))!.report_key);
    const rep = JSON.parse(await obj!.text()) as { checks: { id: string; code?: string }[] };
    expect(rep.checks.find((c) => c.id === "C12")?.code).toBe("flow_member_refused");
  });

  it("a valid new flow goes to a person as a whole, with its chart; one approval countersigns every member and records the flow", async () => {
    const dev = await developer();
    const f = await submitFlow(dev, "chain_two_store");
    for (const sid of f.sids) await runChecks(E, sid);
    for (const sid of f.sids) {
      const r = (await row(sid))!;
      expect(r.status).toBe("in_review");
      expect(JSON.parse(r.route_reasons)).toContain("new_flow");
    }
    const rv = await account();
    await grant(rv.id, "store_reviewer");
    const { cookie } = await addPasskey(rv.cookie);
    const open = await call("GET", `/v1/admin/submissions/${f.sids[0]}`, { cookie });
    const chart = open.body.flow_chart as string;
    expect(chart).toMatch(/^<svg /);
    for (const id of f.ids) expect(chart).toContain(id);
    expect(chart).toContain("silences.found");
    const ok = await call("POST", `/v1/admin/submissions/${f.sids[0]}/approve`, { cookie });
    expect(ok.status).toBe(200);
    expect((ok.body.approved as string[]).sort()).toEqual([...f.sids].sort());
    for (const id of f.ids) {
      const v = await E.DB_CATALOG.prepare("SELECT status, review_level FROM versions WHERE plugin = ?").bind(id).first();
      expect(v).toMatchObject({ status: "approved", review_level: "reviewed" });
    }
    const flow = await E.DB_CATALOG.prepare("SELECT members, events, subscriptions FROM flows WHERE id = ?").bind(f.flowId).first<{ members: string; events: string; subscriptions: string }>();
    expect(JSON.parse(flow!.members).sort()).toEqual([...f.ids].sort());
    expect(JSON.parse(flow!.events)).toContain(`plugin:${f.ids.find((i) => i.endsWith("silences"))}/silences.found`);
    expect(JSON.parse(flow!.subscriptions).length).toBe(1);
    // the listing draws the whole flow and lists the plugin's own events and subscriptions
    await E.CATALOG_WRITER.get(E.CATALOG_WRITER.idFromName("main")).regenerate();
    const { exports } = await import("cloudflare:workers");
    const page = (path: string) => (exports as unknown as { default: Fetcher }).default.fetch(`https://plugins.maktaba.tv${path}`).then((r) => r.text());
    const silences = f.ids.find((i) => i.endsWith("silences"))!;
    const writer = f.ids.find((i) => i.endsWith("chapter-writer"))!;
    const en = await page(`/p/${writer}`);
    expect(en).toContain("<svg");
    expect(en).toContain("How it works with Maktaba and other plugins");
    expect(en).toContain(`plugin:${silences}/silences.found -&gt; ${writer}/write_chapters`);
    expect(await page(`/p/${silences}`)).toContain(`plugin:${silences}/silences.found`);
    const ar = await page(`/ar/p/${writer}`);
    expect(ar).toContain("كيف تعمل مع مكتبة ومع إضافات أخرى");
    expect(ar).toContain("<svg");
  });

  it("the signatures cover the flow: a flow.toml changed after signing is refused at C2", async () => {
    const dev = await developer();
    const m = members(dev, "chain_two_store");
    const p = m.plugins[0]!;
    const files: Record<string, Uint8Array> = { "plugin.wasm": component("good"), "flow.toml": enc.encode(m.flow) };
    for (const path of p.payloads) files[path] = enc.encode('{"type":"object","properties":{}}');
    const signed = await buildPackage(p.id, "1.0.0", dev.key, { manifest: p.manifest, files });
    const sig = (await inspect(signed)).developer_sig!;
    // the same package with one more stage edge in its flow.toml, and the old signature
    const all: Record<string, Uint8Array> = { ...files, "manifest.toml": enc.encode(p.manifest), "i18n/en.json": enc.encode('{"plugin.name":"Tagger","plugin.summary":"Adds tags."}'), "i18n/ar.json": enc.encode('{"plugin.name":"الوسّام","plugin.summary":"يضيف الوسوم."}') };
    all["flow.toml"] = enc.encode(m.flow.replace('triggers = ["audio.ready"]', 'triggers = ["audio.ready", "video.ready"]'));
    const tampered = await pack(all, { "signatures/developer.sig": sig });
    const sid = await submit(dev, p.id, "1.0.0", tampered);
    const r = await runChecks(E, sid);
    expect(r.checks.find((c) => c.result === "block")).toMatchObject({ id: "C2", code: "plugin_signature_invalid" });
  });

  it("a package outside the flow submission's members is refused at the start", async () => {
    const dev = await developer();
    const fs = await call("POST", "/v1/flow-submissions", {
      cookie: dev.cookie,
      body: { flow_id: `${dev.prefix}.flow`, flow_version: "1.0.0", members: [`${dev.prefix}.one`] },
    });
    const other = `${dev.prefix}.two`;
    const pkg = await buildPackage(other, "1.0.0", dev.key, { files: { "plugin.wasm": component("good") } });
    await expect(submit(dev, other, "1.0.0", pkg, { flow_submission: fs.body.id })).rejects.toThrow(/flow_not_member/);
    // another developer's flow submission is not found
    const stranger = await developer();
    const spkg = await buildPackage(`${stranger.prefix}.one`, "1.0.0", stranger.key, { files: { "plugin.wasm": component("good") } });
    await expect(submit(stranger, `${stranger.prefix}.one`, "1.0.0", spkg, { flow_submission: fs.body.id })).rejects.toThrow(/404/);
  });
});
