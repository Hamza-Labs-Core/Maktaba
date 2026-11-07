// Story 54.15 §5, T05, T11 (pairing codes), T12 (the store's half): linking a home with a
// device code, signed home requests, replays and stale signatures refused, unlink.
import { describe, expect, it } from "vitest";
import { b64url } from "../../web/crypto";
import { sha256hex } from "../../web/crypto";
import { MAX_WRONG } from "../../objects/pairing-code";
import { account, call } from "./helpers";

async function homeKey() {
  const k = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"])) as CryptoKeyPair;
  const raw = new Uint8Array((await crypto.subtle.exportKey("raw", k.publicKey)) as ArrayBuffer);
  return { k, public: btoa(String.fromCharCode(...raw)) };
}

async function signed(
  key: CryptoKey,
  homeId: string,
  method: string,
  path: string,
  body: string,
  opts: { ts?: number; nonce?: string } = {},
) {
  const ts = opts.ts ?? Math.floor(Date.now() / 1000);
  const nonce = opts.nonce ?? b64url(crypto.getRandomValues(new Uint8Array(16)));
  const msg = `MKS-Home\n${method}\n${path}\n${ts}\n${nonce}\n${await sha256hex(body)}`;
  const sig = new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, key, new TextEncoder().encode(msg)));
  return { Authorization: `MKS-Home id=${homeId}, ts=${ts}, nonce=${nonce}, sig=${btoa(String.fromCharCode(...sig))}` };
}

async function linked() {
  const admin = await account();
  const hk = await homeKey();
  const p = await call("POST", "/v1/homes/pair", {
    body: { public_key: hk.public, server_version: "0.7.0", platform: "darwin-arm64", name: "Living room" },
  });
  expect(p.status).toBe(200);
  expect(p.body.user_code).toMatch(/^[A-Z0-9]{4}-[A-Z0-9]{4}$/);
  expect(p.body.verification_uri).toBe("https://plugins.maktaba.tv/link");
  expect(String(p.body.verification_uri)).not.toContain(String(p.body.user_code));
  expect((await call("POST", "/v1/homes/pair/poll", { body: { device_code: p.body.device_code } })).body.status).toBe("pending");
  const show = await call("POST", "/v1/homes/link", { cookie: admin.cookie, body: { user_code: p.body.user_code } });
  expect(show.body).toMatchObject({ status: "confirm", name: "Living room", server_version: "0.7.0", platform: "darwin-arm64" });
  const ok = await call("POST", "/v1/homes/link", {
    cookie: admin.cookie,
    body: { user_code: String(p.body.user_code).toLowerCase(), confirm: true },
  });
  expect(ok.status).toBe(200);
  const polled = await call("POST", "/v1/homes/pair/poll", { body: { device_code: p.body.device_code } });
  expect(polled.body).toMatchObject({ status: "linked", home_id: ok.body.home_id });
  return { admin, hk, homeId: ok.body.home_id as string };
}

describe("linking a home", () => {
  it("links with the code, and the home's signed requests are accepted once", async () => {
    const { hk, homeId } = await linked();
    const path = `/v1/homes/${homeId}/seen`;
    const body = JSON.stringify({
      server_version: "0.7.1",
      platform: "darwin-arm64",
      plugins: [{ id: "dev.example.tagger", version: "1.0.0" }],
    });
    const h = await signed(hk.k.privateKey, homeId, "POST", path, body);
    const first = await call("POST", path, { raw: body, headers: h });
    expect(first.status, JSON.stringify(first.body)).toBe(200);
    // the same request again: a replay
    expect((await call("POST", path, { raw: body, headers: h })).body.error).toBe("home_signature_replayed");
  });

  it("T05: a bad, an old and another key's signature are 401", async () => {
    const { hk, homeId } = await linked();
    const other = await homeKey();
    const path = `/v1/homes/${homeId}/seen`;
    const body = JSON.stringify({ plugins: [] });
    const cases = [
      await signed(hk.k.privateKey, homeId, "POST", path, body + " "),
      await signed(hk.k.privateKey, homeId, "POST", path, body, { ts: Math.floor(Date.now() / 1000) - 600 }),
      await signed(other.k.privateKey, homeId, "POST", path, body),
      { Authorization: `Bearer ${homeId}` },
    ];
    for (const h of cases) expect((await call("POST", path, { raw: body, headers: h })).status, JSON.stringify(h)).toBe(401);
  });

  it("T11: the fifth wrong entry kills the code; the right one is then 410 code_expired", async () => {
    const admin = await account();
    const hk = await homeKey();
    const p = await call("POST", "/v1/homes/pair", { body: { public_key: hk.public, name: "Den" } });
    const code = String(p.body.user_code);
    const [head, rest] = code.split("-");
    const wrong = rest === "AAAA" ? "CCCC" : "AAAA";
    for (let i = 0; i < MAX_WRONG; i++) {
      const r = await call("POST", "/v1/homes/link", { cookie: admin.cookie, body: { user_code: `${head}-${wrong}` } });
      expect(r.status).toBe(i < MAX_WRONG - 1 ? 400 : 410);
    }
    expect(await call("POST", "/v1/homes/link", { cookie: admin.cookie, body: { user_code: code, confirm: true } })).toMatchObject({
      status: 410,
      body: { error: "code_expired" },
    });
    expect((await call("POST", "/v1/homes/pair/poll", { body: { device_code: p.body.device_code } })).status).toBe(410);
  });

  it("unlinks from the account; the home's key is then unknown", async () => {
    const { admin, hk, homeId } = await linked();
    expect((await call("GET", "/v1/homes", { cookie: admin.cookie })).body.homes).toHaveLength(1);
    expect((await call("DELETE", `/v1/homes/${homeId}`, { cookie: admin.cookie })).status).toBe(200);
    const path = `/v1/homes/${homeId}/seen`;
    expect((await call("POST", path, { raw: "{}", headers: await signed(hk.k.privateKey, homeId, "POST", path, "{}") })).body.error).toBe(
      "home_unknown",
    );
  });

  it("the home unlinks itself with its signature", async () => {
    const { hk, homeId } = await linked();
    const path = `/v1/homes/${homeId}`;
    const r = await call("DELETE", path, {
      raw: "",
      headers: { ...(await signed(hk.k.privateKey, homeId, "DELETE", path, "")), Origin: "" },
    });
    expect(r.status).toBe(200);
  });
});

describe("the home's Go signer (the shared vector)", () => {
  it("accepts the header the home's Go code makes", async () => {
    const v = (await import("../../../shared/plugins/testdata/signatures/home-request.json")).default as {
      seed: string;
      home_id: string;
      method: string;
      path: string;
      ts: number;
      body: string;
      header: string;
    };
    const { Signer } = await import("../../signing/sig");
    const seed = Uint8Array.from(v.seed.match(/../g)!.map((h) => parseInt(h, 16)));
    const s = await Signer.fromSeed(seed);
    const { E } = await import("./helpers");
    await E.DB_INSTALLS.prepare(
      "INSERT OR REPLACE INTO homes (id, account, public_key, linked_at) VALUES (?, 'a_vector', ?, '2026-11-01T00:00:00Z')",
    )
      .bind(v.home_id, s.public)
      .run();
    const { setClockOffset } = await import("../../web/env");
    const { signedHome } = await import("../../web/homes");
    setClockOffset(v.ts * 1000 - Date.now());
    try {
      const req = new Request(`https://plugins.maktaba.tv${v.path}`, { method: v.method, headers: { Authorization: v.header } });
      const home = await signedHome(E, req, new TextEncoder().encode(v.body));
      expect(home?.id).toBe(v.home_id);
    } finally {
      setClockOffset(0);
    }
  });
});
