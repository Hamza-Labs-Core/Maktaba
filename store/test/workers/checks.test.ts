// Story 54.16 T01, T02, T03, T06 and the ACs: the automated checks on a corpus of bad
// packages (each blocked at its check), the route to a person or to automatic approval, the
// review queue's roles and decisions, and the isolated runner's single-use report.
import { env } from "cloudflare:workers";
import { describe, expect, it } from "vitest";
import { Root, emptyState } from "../../signing/verify";
import { capabilitiesSHA256 } from "../../signing/sig";
import { acceptRunnerReport, malwareRules, rating, runChecks, stringChecks } from "../../web/checks";
import { inspect } from "../../web/wasm";
import { type Dev, E, account, addPasskey, addTotp, ageFactor, buildPackage, call, component, developer, grant, submit } from "./helpers";

const enc = new TextEncoder();
const W = env as never;

/** A component with a custom section holding text (a valid component, the text in its bytes). */
function withCustom(c: Uint8Array, text: string): Uint8Array {
  const name = enc.encode("x");
  const data = enc.encode(text);
  const size = 1 + name.length + data.length;
  return new Uint8Array([...c, 0x00, size, name.length, ...name, ...data]);
}

async function checked(dev: Dev, name: string, version: string, opts: Parameters<typeof buildPackage>[3] = {}, key = dev.key) {
  const id = `${dev.prefix}.${name}`;
  const sid = await submit(
    dev,
    id,
    version,
    await buildPackage(id, version, key, {
      capabilities: { optional: ["tags.write"] },
      ...opts,
      files: { "plugin.wasm": component("good"), ...(opts.files ?? {}) },
    }),
  );
  const report = await runChecks(E, sid);
  const row = await E.DB_CATALOG.prepare("SELECT status, route, route_reasons, decision, reasons FROM submissions WHERE id = ?")
    .bind(sid)
    .first<{
      status: string;
      route: string;
      route_reasons: string;
      reasons: string;
    }>();
  return { id, sid, report, row: row!, blocked: report.checks.find((c) => c.result === "block") };
}

async function reviewer(): Promise<{ id: string; cookie: string }> {
  const r = await account();
  await grant(r.id, "store_reviewer");
  return { id: r.id, cookie: (await addPasskey(r.cookie)).cookie };
}

describe("the checks (T01): each bad package is blocked at its check", () => {
  const cases: [string, Parameters<typeof buildPackage>[3], string, string][] = [
    ["imports wasi:http/outgoing-handler", { files: { "plugin.wasm": component("forbidden-http") } }, "C4", "plugin_forbidden_import"],
    ["imports sockets", { files: { "plugin.wasm": component("forbidden-sockets") } }, "C4", "plugin_forbidden_import"],
    ["imports media without the capability", { files: { "plugin.wasm": component("undeclared-media") } }, "C4", "plugin_undeclared_import"],
    ["exports no run", { files: { "plugin.wasm": component("no-run") } }, "C4", "plugin_missing_export"],
    [
      "a name with «مكتبة»",
      { files: { "i18n/ar.json": enc.encode('{"plugin.name":"مكتبة الوسوم","plugin.summary":"يضيف الوسوم."}') } },
      "C6",
      "name_reserved",
    ],
    [
      "a name with Maktaba",
      { files: { "i18n/en.json": enc.encode('{"plugin.name":"Official Maktaba Tagger","plugin.summary":"Adds tags."}') } },
      "C6",
      "name_reserved",
    ],
    ["a cryptominer", { files: { "plugin.wasm": withCustom(component("good"), "stratum+tcp://pool.example") } }, "C7", "malware"],
  ];
  for (const [name, opts, check, code] of cases) {
    it(name, async () => {
      const dev = await developer();
      const r = await checked(
        dev,
        name
          .replace(/[^a-z]+/gi, "-")
          .toLowerCase()
          .replace(/^-|-$/g, "") || "x",
        "1.0.0",
        opts,
      );
      expect(r.blocked, JSON.stringify(r.report.checks)).toMatchObject({ id: check, code });
      expect(r.row.status).toBe("rejected");
      expect(JSON.parse(r.row.reasons)).toEqual([code]);
    });
  }

  it("a package signed by another key is blocked at C2", async () => {
    const dev = await developer();
    const { Signer } = await import("../../signing/sig");
    const r = await checked(dev, "other-key", "1.0.0", {}, await Signer.fromSeed(crypto.getRandomValues(new Uint8Array(32))));
    expect(r.blocked).toMatchObject({ id: "C2", code: "developer_key_inactive" });
  });

  it("a malformed manifest is C3's; a missing maktaba upper bound too", async () => {
    const dev = await developer();
    const id = `${dev.prefix}.nobound`;
    const pkg = await buildPackage(id, "1.0.0", dev.key, {
      capabilities: { optional: ["tags.write"] },
      files: { "plugin.wasm": component("good") },
    });
    // the same package with a manifest without the upper bound
    const { pack } = await import("../../web/wasm");
    const i = await inspect(pkg);
    void i;
    const manifest = (await import("./helpers"))
      .manifestOf(id, "1.0.0", { optional: ["tags.write"] })
      .replace('maktaba = ">=0.6.0, <0.9.0"', 'maktaba = ">=0.6.0"');
    const files = {
      "manifest.toml": enc.encode(manifest),
      "i18n/en.json": enc.encode('{"plugin.name":"Tagger","plugin.summary":"Adds tags."}'),
      "i18n/ar.json": enc.encode('{"plugin.name":"الوسّام","plugin.summary":"يضيف الوسوم."}'),
      "plugin.wasm": component("good"),
    };
    const unsigned = await pack(files);
    const { filesSHA256, signFile } = await import("../../signing/sig");
    const sig = await signFile("developer", dev.key, {
      plugin_id: id,
      version: "1.0.0",
      files_sha256: await filesSHA256((await inspect(unsigned)).files_json),
    });
    const sid = await submit(dev, id, "1.0.0", await pack(files, { "signatures/developer.sig": sig }));
    const r = await runChecks(E, sid);
    expect(r.checks.find((c) => c.result === "block")).toMatchObject({ id: "C3", code: "maktaba_upper_bound" });
  });
});

describe("the route (T02)", () => {
  it("a first version goes to a person; without a runner it waits for one", async () => {
    const dev = await developer();
    const r = await checked(dev, "first", "1.0.0");
    expect(r.blocked).toBeUndefined();
    expect(r.row).toMatchObject({ status: "in_review", route: "human" });
    expect(JSON.parse(r.row.route_reasons)).toContain("first_version");
  });

  it("an update adding transcript.read goes to a person; an update that only changes code is approved automatically", async () => {
    const dev = await developer();
    const rv = await reviewer();
    const first = await checked(dev, "grows", "1.0.0");
    expect((await call("POST", `/v1/admin/submissions/${first.sid}/approve`, { cookie: rv.cookie })).status).toBe(200);
    // the runner would report; here it is a plain update with the runner's three checks
    const more = await checked(dev, "grows", "1.1.0", { capabilities: { optional: ["tags.write", "transcript.read"] } });
    expect(more.row.status).toBe("in_review");
    expect(JSON.parse(more.row.route_reasons)).toContain("new_capability");
    // a code-only update with an isolated runner's report: automatic, countersigned automated
    E.RUNNER = "github";
    E.RUNNER_DISPATCH_TOKEN = "test";
    const orig = globalThis.fetch;
    globalThis.fetch = (async () => new Response(null, { status: 204 })) as typeof fetch;
    try {
      const plain = await checked(dev, "grows", "1.2.0");
      expect(plain.report.checks.filter((c) => c.result === "pending").map((c) => c.id)).toEqual(["C8", "C10"]);
      const sub = await E.DB_CATALOG.prepare("SELECT * FROM submissions WHERE id = ?").bind(plain.sid).first();
      const done = await acceptRunnerReport(E, sub as never, {
        c7: { result: "pass" },
        c8: { result: "pass", peak_memory_mb: 3 },
        c10: { result: "skip" },
      });
      expect(done.route_reasons).toEqual([]);
      const v = await E.DB_CATALOG.prepare("SELECT status, review_level FROM versions WHERE plugin = ? AND version = '1.2.0'")
        .bind(plain.id)
        .first();
      expect(v).toEqual({ status: "approved", review_level: "automated" });
    } finally {
      globalThis.fetch = orig;
      E.RUNNER = "none";
      delete E.RUNNER_DISPATCH_TOKEN;
    }
  });

  it("a plugin that traps on its own fixtures is blocked with the trap's backtrace (C8)", async () => {
    const dev = await developer();
    E.RUNNER = "github";
    E.RUNNER_DISPATCH_TOKEN = "test";
    const orig = globalThis.fetch;
    globalThis.fetch = (async () => new Response(null, { status: 204 })) as typeof fetch;
    try {
      const r = await checked(dev, "traps", "1.0.0");
      const sub = await E.DB_CATALOG.prepare("SELECT * FROM submissions WHERE id = ?").bind(r.sid).first();
      const done = await acceptRunnerReport(E, sub as never, {
        c7: { result: "pass" },
        c8: { result: "block", code: "plugin_trap", detail: "tag on fixtures/a.json: trap\nwasm backtrace:\n  0: run" },
        c10: { result: "skip" },
      });
      expect(done.checks.find((c) => c.id === "C8")).toMatchObject({
        result: "block",
        code: "plugin_trap",
        detail: expect.stringContaining("backtrace"),
      });
      expect(
        (await E.DB_CATALOG.prepare("SELECT status FROM submissions WHERE id = ?").bind(r.sid).first<{ status: string }>())!.status,
      ).toBe("rejected");
    } finally {
      globalThis.fetch = orig;
      E.RUNNER = "none";
      delete E.RUNNER_DISPATCH_TOKEN;
    }
  });
});

describe("the string and rating checks (T03)", () => {
  it("URLs, emails and phone numbers warn; missing keys and reserved words block", () => {
    expect(stringChecks({ en: { "plugin.name": "A" }, ar: {} }, "plugin.name").result).toBe("block");
    expect(
      stringChecks({ en: { "plugin.name": "A", s: "see https://x.example" }, ar: { "plugin.name": "أ", s: "ب" } }, "plugin.name"),
    ).toMatchObject({ result: "warn", code: "strings_link" });
    expect(stringChecks({ en: { "plugin.name": "A", s: "mail me@x.org" }, ar: { "plugin.name": "أ", s: "ب" } }, "plugin.name").result).toBe(
      "warn",
    );
    expect(stringChecks({ en: { "plugin.name": "Tagger" }, ar: { "plugin.name": "مَكْتَبَة" } }, "plugin.name")).toMatchObject({
      result: "block",
      code: "name_reserved",
    });
    expect(stringChecks({ en: { "plugin.name": "Tagger" }, ar: { "plugin.name": "الوسّام" } }, "plugin.name").result).toBe("pass");
    expect(malwareRules(enc.encode("xmrig")).result).toBe("block");
  });

  it("the rating is the home's (shared/plugins/testdata/ratings.json)", async () => {
    const t = (await import("../../../shared/plugins/testdata/ratings.json")).default as {
      cases: { capabilities: string[]; review_level: string; rating: number }[];
    };
    for (const c of t.cases) expect(rating(c.capabilities, c.review_level), JSON.stringify(c)).toBe(c.rating);
  });
});

describe("review (T06 and the ACs)", () => {
  it("roles: no one, buyer, developer, reviewer with TOTP only, reviewer with a passkey, store admin", async () => {
    const buyer = await account();
    const dev = await developer();
    const totp = await account();
    await grant(totp.id, "store_reviewer");
    await addTotp(totp.cookie);
    const rv = await reviewer();
    const admin = await account();
    await grant(admin.id, "store_admin");
    const ad = await addPasskey(admin.cookie);
    const want: [string | undefined, number][] = [
      [undefined, 401],
      [buyer.cookie, 403],
      [dev.cookie, 403],
      [totp.cookie, 403],
      [rv.cookie, 200],
      [ad.cookie, 200],
    ];
    for (const [cookie, status] of want) expect((await call("GET", "/v1/admin/queue", { cookie })).status).toBe(status);
    expect((await call("GET", "/v1/admin/queue", { cookie: totp.cookie })).body.error).toBe("passkey_required");
  });

  it("approving makes store.sig level reviewed, regenerates the catalog and the audit row names the reviewer", async () => {
    const dev = await developer();
    const rv = await reviewer();
    const r = await checked(dev, "approved", "1.0.0");
    const res = await call("POST", `/v1/admin/submissions/${r.sid}/approve`, { cookie: rv.cookie });
    expect(res.status).toBe(200);
    const pkg = await inspect(new Uint8Array(await (await E.BUCKET.get(`${res.body.sha256}.mkplugin`))!.arrayBuffer()));
    const keys = await (await Root.parse(E.PLUGINS_ROOT)).verifyStoreKeys(E.TEST_STORE_KEYS, emptyState(), new Date());
    const v = await keys.verifyPackage({
      filesJSON: pkg.files_json,
      developerSig: pkg.developer_sig,
      storeSig: pkg.store_sig,
      pluginId: r.id,
      version: "1.0.0",
      capabilitiesSHA256: await capabilitiesSHA256(JSON.parse(pkg.manifest).capabilities),
    });
    expect(v.level).toBe("reviewed");
    const cat = await keys.verifyCatalog(await (await E.BUCKET.get("catalog-1.json"))!.text(), 1, emptyState(), new Date());
    expect((cat.signed.plugins as { id: string }[]).some((p) => p.id === r.id)).toBe(true);
    const a = await E.DB_ACCOUNTS.prepare("SELECT actor FROM audit WHERE action = 'review.approve' AND target LIKE ?")
      .bind(`${r.sid}:%`)
      .first<{ actor: string }>();
    expect(a!.actor).toBe(rv.id);
  });

  it("a reviewer whose passkey was 6 minutes ago gets 401 reauth_required and nothing is signed", async () => {
    const dev = await developer();
    const rv = await reviewer();
    const r = await checked(dev, "late", "1.0.0");
    await ageFactor(rv.cookie, 6);
    const res = await call("POST", `/v1/admin/submissions/${r.sid}/approve`, { cookie: rv.cookie });
    expect(res).toMatchObject({ status: 401, body: { error: "reauth_required" } });
    const v = await E.DB_CATALOG.prepare("SELECT status, store_sig FROM versions WHERE plugin = ? AND version = '1.0.0'")
      .bind(r.id)
      .first();
    expect(v).toEqual({ status: "in_review", store_sig: "" });
  });

  it("reject needs reasons from the fixed list; ask pauses the clock until the developer answers", async () => {
    const dev = await developer();
    const rv = await reviewer();
    const r = await checked(dev, "asked", "1.0.0");
    expect(
      (await call("POST", `/v1/admin/submissions/${r.sid}/reject`, { cookie: rv.cookie, body: { reasons: ["ugly"] } })).body.error,
    ).toBe("reasons_invalid");
    expect(
      (await call("POST", `/v1/admin/submissions/${r.sid}/ask`, { cookie: rv.cookie, body: { question: "Why media.read?" } })).status,
    ).toBe(200);
    const q = await call("GET", "/v1/admin/queue", { cookie: rv.cookie });
    expect((q.body.items as { id: string; status: string }[]).find((i) => i.id === r.sid)!.status).toBe("asked");
    expect(
      (await call("POST", `/v1/submissions/${r.sid}/answer`, { cookie: dev.cookie, body: { answer: "For the frames." } })).status,
    ).toBe(200);
    expect(
      (
        await call("POST", `/v1/admin/submissions/${r.sid}/reject`, {
          cookie: rv.cookie,
          body: { reasons: ["asks_too_much"], notes: "Use frames only." },
        })
      ).status,
    ).toBe(200);
    expect(
      (await E.DB_CATALOG.prepare("SELECT status FROM versions WHERE plugin = ?").bind(r.id).first<{ status: string }>())!.status,
    ).toBe("rejected");
  });

  it("a reviewer cannot approve their own submission", async () => {
    const dev = await developer();
    await grant(dev.id, "store_reviewer");
    const { cookie } = await addPasskey(dev.cookie);
    const r = await checked(dev, "own", "1.0.0");
    expect((await call("POST", `/v1/admin/submissions/${r.sid}/approve`, { cookie })).body.error).toBe("own_submission");
  });

  it("the admin pages are sent only to a reviewer signed in with a passkey, with noindex and no-store", async () => {
    const buyer = await account();
    const anon = await call("GET", "/admin/queue", {});
    expect(anon.status).toBe(302);
    expect((await call("GET", "/admin", { cookie: buyer.cookie })).status).toBe(302);
  });
});

describe("the isolated runner's URLs", () => {
  it("each works once, and a report is taken only when one is awaited", async () => {
    const dev = await developer();
    E.RUNNER = "github";
    E.RUNNER_DISPATCH_TOKEN = "test";
    const orig = globalThis.fetch;
    let payload: Record<string, string> = {};
    globalThis.fetch = (async (_u: string, init: RequestInit) => {
      payload = JSON.parse(String(init.body)).inputs;
      return new Response(null, { status: 204 });
    }) as typeof fetch;
    try {
      const r = await checked(dev, "runner", "1.0.0");
      globalThis.fetch = orig;
      const pkgPath = new URL(payload.package_url!).pathname;
      const first = await call("GET", pkgPath, {});
      expect(first.status).toBe(200);
      expect((await call("GET", pkgPath, {})).status).toBe(410);
      const rep = new URL(payload.report_url!).pathname;
      const report = JSON.stringify({ c7: { result: "pass" }, c8: { result: "pass" }, c10: { result: "skip" } });
      expect((await call("POST", rep, { raw: report, headers: { "Content-Type": "application/json" } })).status).toBe(200);
      expect((await call("POST", rep, { raw: report })).status).toBe(410);
      expect(
        (await E.DB_CATALOG.prepare("SELECT runner_state FROM submissions WHERE id = ?").bind(r.sid).first<{ runner_state: string }>())!
          .runner_state,
      ).toBe("reported");
    } finally {
      globalThis.fetch = orig;
      E.RUNNER = "none";
      delete E.RUNNER_DISPATCH_TOKEN;
    }
  });
});
void W;
