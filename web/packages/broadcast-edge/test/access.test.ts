// Story 50.26: viewer access. T01 invites, T02 short codes, T03 Turnstile, T04 app attestation,
// T05 play and beat, T06 limits, T07 no payment route, T08 1,000 sessions for 10 minutes,
// T09 the viewer strings in English and Arabic; and the acceptance criteria.
import { afterEach, describe, expect, it } from "vitest";
import { advanceClock, now, resetClock } from "../src/clock";
import type { Env } from "../src/env";
import { INVITE_DOMAIN, INVITE_PREFIX, verifyInvite, type InviteClaims } from "../src/invites";
import { WATCH_ROUTES } from "../src/play";
import { forgetRevocations } from "../src/revocations";
import { ERROR_KEYS, STRINGS } from "../src/strings";
import { verifyViewer } from "../src/tokens";
import { call, cloudKid, control, E, INGEST, LIVE, newChannel, signCloud } from "./helpers";

afterEach(() => {
  resetClock();
  forgetRevocations();
});

const WATCH = "https://watch.maktaba.tv";
const env = E as Env;

let pubN = 0;
function newPub(): string {
  pubN++;
  return `0b9a3c5e-1111-4222-8333-${(Date.now() % 1e9).toString().padStart(9, "0")}${String(pubN).padStart(3, "0")}`;
}

async function invite(
  over: Partial<InviteClaims> & { pub: string },
  label = "cloud-1"
): Promise<string> {
  const t = Math.floor(now() / 1000);
  return signCloud(
    INVITE_PREFIX,
    INVITE_DOMAIN,
    {
      typ: "invite",
      chs: [],
      inv: `inv-${Math.random().toString(36).slice(2)}`,
      max: 10,
      iat: t,
      exp: t + 30 * 86400,
      kid: await cloudKid(label),
      ...over,
    },
    label
  );
}

interface Setup {
  pub: string;
  ch: string;
  ch2: string;
}

let version = 1;
async function access(pub: string, over: Record<string, unknown> = {}): Promise<Response> {
  return call(`${INGEST}/v1/control/access`, {
    method: "PUT",
    body: await control("access", {
      pub,
      version: version++,
      channels: {},
      open_allowed: false,
      max_sessions_per_ip: 3,
      daily_minutes: 0,
      revoked_invites: [],
      revoked_passes: [],
      codes: {},
      ...over,
    }),
  });
}

async function plan(pub: string, over: Record<string, unknown> = {}): Promise<void> {
  const r = await call(`${INGEST}/v1/control/plan`, {
    method: "PUT",
    body: await control("plan", {
      pub,
      mode: "full",
      version: version++,
      max_viewers: 100,
      ...over,
    }),
  });
  expect(r.status).toBe(200);
}

async function setup(
  chans: Record<string, unknown> = {},
  over: Record<string, unknown> = {}
): Promise<Setup> {
  const pub = newPub();
  const [ch, ch2] = [newChannel(), newChannel()];
  const r = await access(pub, {
    channels: {
      [ch]: { access: "invite", max_viewers: 0, indexable: false },
      [ch2]: { access: "open", max_viewers: 0, indexable: false },
      ...chans,
    },
    open_allowed: true,
    ...over,
  });
  expect(r.status).toBe(200);
  await plan(pub);
  return { pub, ch, ch2 };
}

function post(
  path: string,
  body: unknown,
  headers: Record<string, string> = {},
  envOver: Partial<Env> = {}
) {
  return call(
    `${WATCH}${path}`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "CF-Connecting-IP": "198.51.100.7",
        ...headers,
      },
      body: JSON.stringify(body),
    },
    envOver
  );
}

async function redeem(token: string, headers: Record<string, string> = {}) {
  const r = await post("/api/v1/redeem", { invite: token }, headers);
  return { r, j: (await r.json()) as Record<string, string> };
}

const beatKeys = new Map<string, string>();

async function playWith(s: { pub: string }, ch: string, auth: string, ip = "198.51.100.7") {
  const r = await post(
    "/api/v1/play",
    { publisher: s.pub, channel: ch },
    auth ? { Authorization: `Bearer ${auth}`, "CF-Connecting-IP": ip } : { "CF-Connecting-IP": ip }
  );
  const j = (await r.json()) as Record<string, unknown>;
  if (typeof j.session_id === "string") beatKeys.set(j.session_id, j.beat_key as string);
  return { r, j };
}

async function beatOf(pub: string, session: string, key = beatKeys.get(session)) {
  const r = await post(`/api/v1/play/${session}/beat`, {
    publisher: pub,
    beat_key: key,
    rung: "720p",
    stats: {},
  });
  return { r, j: (await r.json()) as Record<string, unknown> };
}

async function guest(s: Setup, over: Partial<InviteClaims> = {}): Promise<string> {
  const { r, j } = await redeem(await invite({ pub: s.pub, ...over }));
  expect(r.status).toBe(200);
  return j.pass!;
}

describe("T01 invite verify", () => {
  it("good / expired / bad signature / uses exhausted / revoked", async () => {
    const s = await setup();
    // good: a guest pass, as a cookie and in the body, and one use counted
    const good = await invite({ pub: s.pub, max: 2 });
    const a = await redeem(good);
    expect(a.r.status).toBe(200);
    expect(a.j.pass).toMatch(/^mkbg1\./);
    expect(a.r.headers.get("Set-Cookie")).toMatch(
      new RegExp(`^mkp_${s.pub}=mkbg1\\..*HttpOnly; Secure; SameSite=Lax`)
    );
    // expired: 410
    const t = Math.floor(now() / 1000);
    const old = await invite({ pub: s.pub, iat: t - 100, exp: t - 1 });
    const e = await redeem(old);
    expect(e.r.status).toBe(410);
    expect(e.j.error).toBe("invite_expired");
    expect(e.j.key).toBe("broadcast.invite.expired");
    // bad signature: 401 (signed with a key the edge doesn't hold)
    expect((await redeem(await invite({ pub: s.pub }, "not-a-cloud-key"))).r.status).toBe(401);
    expect((await redeem(good.slice(0, -3) + "AAA")).r.status).toBe(401);
    // AC: 2 uses left, three devices: two passes, the third sees broadcast.invite.used
    const b = await redeem(good);
    expect(b.r.status).toBe(200);
    const c = await redeem(good);
    expect(c.r.status).toBe(410);
    expect(c.j.error).toBe("invite_used");
    expect(c.j.key).toBe("broadcast.invite.used");
    // the same device again (holding its pass) uses nothing
    const again = await redeem(good, { Cookie: `mkp_${s.pub}=${a.j.pass}` });
    expect(again.r.status).toBe(200);
    expect(again.j.reused).toBe(true);
    // revoked: 410
    const inv = `inv-revoked-${pubN}`;
    await access(s.pub, {
      revoked_invites: [inv],
      channels: { [s.ch]: { access: "invite", max_viewers: 0 } },
    });
    const rv = await redeem(await invite({ pub: s.pub, inv }));
    expect(rv.r.status).toBe(410);
    expect(rv.j.error).toBe("invite_revoked");
  });

  it("checks the cloud's own invite vector", async () => {
    const v = JSON.parse(E.TEST_INVITE_VECTORS) as { at_ms: number; invite: string };
    const r = await verifyInvite(v.invite, env.CLOUD_PUBKEYS, v.at_ms);
    expect(r.ok).toBe(true);
    if (r.ok) expect(r.claims.pub).toBe("srv-1");
  });

  it("the invite page shows a button and redeems only on the form's post", async () => {
    const s = await setup();
    const tok = await invite({ pub: s.pub, max: 1 });
    const page = await call(`${WATCH}/i/${tok}`);
    expect(page.status).toBe(200);
    const html = await page.text();
    expect(html).toContain('dir="rtl"');
    expect(html).toContain('name="robots" content="noindex"');
    expect(html).toContain("المشاهدة مجانية");
    expect(page.headers.get("X-Server-Time")).toMatch(/^\d+$/);
    // opening the link twice used nothing: the form's post still redeems
    await call(`${WATCH}/i/${tok}`);
    const done = await call(`${WATCH}/i/${tok}`, {
      method: "POST",
      headers: { "Accept-Language": "en" },
    });
    expect(done.status).toBe(200);
    expect(done.headers.get("Set-Cookie")).toContain(`mkp_${s.pub}=`);
    expect(await done.text()).toContain("Open the Maktaba Player");
    const used = await call(`${WATCH}/i/${tok}`, {
      method: "POST",
      headers: { "Accept-Language": "en" },
    });
    expect(used.status).toBe(410);
    expect(await used.text()).toContain("already been used");
  });
});

describe("T02 short codes", () => {
  it("redeem, a wrong code, 11 tries in a minute", async () => {
    const tok = await invite({ pub: "x" });
    const s = await setup({}, {});
    const real = await invite({ pub: s.pub });
    await access(s.pub, {
      codes: { "MK-AB12": real },
      channels: { [s.ch]: { access: "invite", max_viewers: 0 } },
    });
    expect(tok).toBeTruthy();
    const ok = await post(
      "/api/v1/redeem",
      { publisher: s.pub, code: "mk-ab12" },
      { "CF-Connecting-IP": "203.0.113.1" }
    );
    expect(ok.status).toBe(200);
    const wrong = await post(
      "/api/v1/redeem",
      { publisher: s.pub, code: "MK-ZZ99" },
      { "CF-Connecting-IP": "203.0.113.1" }
    );
    expect(wrong.status).toBe(404);
    for (let i = 0; i < 8; i++)
      await post(
        "/api/v1/redeem",
        { publisher: s.pub, code: "MK-ZZ99" },
        { "CF-Connecting-IP": "203.0.113.1" }
      );
    // the 11th try in the minute, even with the right code
    const limited = await post(
      "/api/v1/redeem",
      { publisher: s.pub, code: "MK-AB12" },
      { "CF-Connecting-IP": "203.0.113.1" }
    );
    expect(limited.status).toBe(429);
    // another network still may; a minute later this one may again
    expect(
      (
        await post(
          "/api/v1/redeem",
          { publisher: s.pub, code: "MK-AB12" },
          { "CF-Connecting-IP": "203.0.113.2" }
        )
      ).status
    ).toBe(200);
    advanceClock(61_000);
    expect(
      (
        await post(
          "/api/v1/redeem",
          { publisher: s.pub, code: "MK-AB12" },
          { "CF-Connecting-IP": "203.0.113.1" }
        )
      ).status
    ).toBe(200);
  });

  it("caps a publisher's failed tries from every network together", async () => {
    const s = await setup();
    await access(s.pub, {
      codes: { "MK-AB12": await invite({ pub: s.pub }) },
      channels: { [s.ch]: { access: "invite", max_viewers: 0 } },
    });
    for (let i = 0; i < 60; i++)
      await post(
        "/api/v1/redeem",
        { publisher: s.pub, code: "MK-ZZ99" },
        { "CF-Connecting-IP": `192.0.2.${i}` }
      );
    const r = await post(
      "/api/v1/redeem",
      { publisher: s.pub, code: "MK-AB12" },
      { "CF-Connecting-IP": "192.0.2.200" }
    );
    expect(r.status).toBe(429);
  });
});

describe("T03 Turnstile", () => {
  it("ok / fail / replay; and an open channel without it is 403 turnstile_required", async () => {
    const s = await setup();
    // AC: an open channel, no Turnstile
    const none = await playWith(s, s.ch2, "");
    expect(none.r.status).toBe(403);
    expect(none.j.error).toBe("turnstile_required");
    // an invite channel without a pass
    expect((await playWith(s, s.ch, "")).j.error).toBe("invite_required");
    const ok = await post("/api/v1/open/turnstile", { token: "good-1" });
    expect(ok.status).toBe(200);
    const j = (await ok.json()) as { session: string };
    expect(j.session).toMatch(/^mkbo1\./);
    expect(ok.headers.get("Set-Cookie")).toMatch(/^mko=mkbo1\..*Max-Age=43200; HttpOnly/);
    expect((await post("/api/v1/open/turnstile", { token: "bad" })).status).toBe(403);
    expect((await post("/api/v1/open/turnstile", { token: "good-1" })).status).toBe(403);
    // the open session plays the open channel, not the invite one
    expect((await playWith(s, s.ch2, j.session)).r.status).toBe(200);
    expect((await playWith(s, s.ch, j.session)).j.error).toBe("invite_required");
    // not verified, or the flag off: the open channel is invite-only for new sessions
    await access(s.pub, {
      open_allowed: false,
      channels: { [s.ch2]: { access: "open", max_viewers: 0 } },
    });
    expect((await playWith(s, s.ch2, j.session)).j.error).toBe("invite_required");
    // without a secret, no open sessions
    expect(
      (
        await post(
          "/api/v1/open/turnstile",
          { token: "good-2" },
          {},
          { TURNSTILE_SECRET: undefined }
        )
      ).status
    ).toBe(503);
  });
});

describe("T04 app attestation", () => {
  const challenge = async () =>
    ((await (await post("/api/v1/open/challenge", {})).json()) as { nonce: string }).nonce;

  it("a fake verifier: session / 403; a challenge once; none bound: 503", async () => {
    const n1 = await challenge();
    const ok = await post("/api/v1/open/attest", {
      platform: "apple",
      attestation: "valid-abc",
      nonce: n1,
    });
    expect(ok.status).toBe(200);
    expect(((await ok.json()) as { session: string }).session).toMatch(/^mkbo1\./);
    // the same challenge again, none, or one the edge never gave: 403
    for (const nonce of [n1, "", "made-up"]) {
      const r = await post("/api/v1/open/attest", {
        platform: "apple",
        attestation: "valid-abc",
        nonce,
      });
      expect(r.status).toBe(403);
    }
    expect(
      (
        await post("/api/v1/open/attest", {
          platform: "android",
          attestation: "forged",
          nonce: await challenge(),
        })
      ).status
    ).toBe(403);
    // a forged attestation spends nothing: the same challenge still serves a real one once,
    // and one with its mac changed serves none
    const n2 = await challenge();
    const attest = (attestation: string, nonce: string) =>
      post("/api/v1/open/attest", { platform: "android", attestation, nonce });
    expect((await attest("forged", n2)).status).toBe(403);
    const tampered = n2.replace(/.$/, (c) => (c === "A" ? "B" : "A"));
    expect((await attest("valid-abc", tampered)).status).toBe(403);
    expect((await attest("valid-abc", n2)).status).toBe(200);
    expect((await attest("valid-abc", n2)).status).toBe(403);
    expect(
      (
        await post("/api/v1/open/attest", {
          platform: "tizen",
          attestation: "valid-abc",
          nonce: await challenge(),
        })
      ).status
    ).toBe(400);
    // a challenge lives 5 minutes
    const late = await challenge();
    advanceClock(5 * 60_000 + 1);
    expect(
      (
        await post("/api/v1/open/attest", {
          platform: "apple",
          attestation: "valid-abc",
          nonce: late,
        })
      ).status
    ).toBe(403);
    expect(
      (
        await post(
          "/api/v1/open/attest",
          { platform: "apple", attestation: "valid-abc", nonce: await challenge() },
          {},
          { ATTEST_API: undefined }
        )
      ).status
    ).toBe(503);
  });
});

describe("T05 play and beat", () => {
  it("refreshes the token after half its life; 150 s of silence ends the session", async () => {
    const s = await setup();
    const pass = await guest(s);
    const p = await playWith(s, s.ch, pass);
    expect(p.r.status).toBe(200);
    expect(p.r.headers.get("X-Server-Time")).toMatch(/^\d+$/);
    expect(p.j.state).toBe("full");
    expect(p.j.refresh_after).toBe(300);
    const master = p.j.master_url as string;
    expect(master).toMatch(
      new RegExp(`^https://live\\.maktaba\\.tv/mkbv1\\.[^/]+/${s.ch}/master\\.m3u8$`)
    );
    const token = master.split("/")[3]!;
    const v = await verifyViewer(token, env.VIEWER_TOKEN_KEY, now(), s.ch);
    expect(
      v.ok &&
        v.claims.session === p.j.session_id &&
        v.claims.pub === s.pub &&
        v.claims.mode === "full"
    ).toBe(true);
    // neighbours: the pass opens every channel, manifests only
    expect((p.j.neighbours as { channel: string }[]).map((n) => n.channel)).toEqual([s.ch2]);
    const session = p.j.session_id as string;
    for (let i = 1; i <= 4; i++) {
      advanceClock(60_000);
      const b = await beatOf(s.pub, session);
      expect(b.r.status).toBe(200);
      expect(b.j.master_url).toBeUndefined();
    }
    advanceClock(60_000); // 300 s: half the token's life
    const refreshed = await beatOf(s.pub, session);
    expect(refreshed.j.master_url).toBeDefined();
    // 150 s without a beat: ended, no token minted
    advanceClock(151_000);
    const ended = await beatOf(s.pub, session);
    expect(ended.r.status).toBe(410);
    expect(ended.j.error).toBe("session_ended");
    expect(ended.j.key).toBe("broadcast.ended");
    // the old token's segments stop after its 10 minutes
    advanceClock(10 * 60_000);
    expect((await call(`${LIVE}/${token}/${s.ch}/master.m3u8`)).status).toBe(401);
  });

  it("AC: a revoked invite's heartbeat fails; so does a revoked device's pass", async () => {
    const s = await setup();
    const inv = `inv-a-${pubN}`;
    const pass = await guest(s, { inv });
    const other = await guest(s);
    const a = await playWith(s, s.ch, pass);
    const b = await playWith(s, s.ch, other);
    await access(s.pub, {
      revoked_invites: [inv],
      channels: { [s.ch]: { access: "invite", max_viewers: 0 } },
    });
    advanceClock(60_000);
    const ra = await beatOf(s.pub, a.j.session_id as string);
    expect(ra.r.status).toBe(410);
    expect(ra.j.error).toBe("revoked");
    expect((await beatOf(s.pub, b.j.session_id as string)).r.status).toBe(200);
    // the revoked pass gets no new session either
    expect((await playWith(s, s.ch, pass)).r.status).toBe(403);
    // one device's pass
    const passes = (await (
      await call(`${INGEST}/v1/control/passes`, {
        method: "POST",
        body: await control("passes", { pub: s.pub }),
      })
    ).json()) as {
      passes: { id: string; inv: string }[];
      uses: Record<string, number>;
    };
    expect(passes.uses[inv]).toBe(1);
    const otherId = passes.passes.find((x) => x.inv !== inv)!.id;
    await access(s.pub, {
      revoked_passes: [otherId],
      channels: { [s.ch]: { access: "invite", max_viewers: 0 } },
    });
    expect((await beatOf(s.pub, b.j.session_id as string)).r.status).toBe(410);
  });

  it("a beat needs the session's beat key; one device holds one session", async () => {
    const s = await setup();
    const pass = await guest(s);
    const p = await playWith(s, s.ch, pass);
    expect(p.j.beat_key).toBeTruthy();
    expect(JSON.stringify(p.j.master_url)).not.toContain(p.j.beat_key as string);
    advanceClock(60_000);
    // the session id is readable in the playback URL; without the key it keeps nothing alive
    const stolen = await beatOf(s.pub, p.j.session_id as string, "guessed");
    expect(stolen.r.status).toBe(410);
    expect(stolen.j.master_url).toBeUndefined();
    expect((await beatOf(s.pub, p.j.session_id as string)).r.status).toBe(200);
    // the same pass plays again (the app restarted): its earlier session ends, one is counted
    const again = await playWith(s, s.ch2, pass);
    expect(again.r.status).toBe(200);
    expect((await beatOf(s.pub, p.j.session_id as string)).r.status).toBe(410);
    expect((await env.VIEWER.get(env.VIEWER.idFromName(s.pub)).concurrent()).total).toBe(1);
  });

  it("an invite for one channel opens only that channel", async () => {
    const s = await setup();
    const pass = await guest(s, { chs: [s.ch] });
    expect((await playWith(s, s.ch, pass)).r.status).toBe(200);
    expect((await playWith(s, s.ch2, pass)).j.error).toBe("not_invited");
    // a pass from another publisher opens nothing here
    const t = await setup();
    const theirs = await guest(t);
    expect((await playWith(s, s.ch, theirs)).j.error).toBe("invite_required");
  });
});

describe("T06 limits", () => {
  it("AC: the publisher's limit: the next viewer gets 429 broadcast_full, nobody playing is affected", async () => {
    const s = await setup();
    await plan(s.pub, { max_viewers: 2 });
    const pass = await guest(s);
    const a = await playWith(s, s.ch, pass);
    const b = await playWith(s, s.ch2, await guest(s));
    const c = await playWith(s, s.ch, await guest(s));
    expect([a.r.status, b.r.status]).toEqual([200, 200]);
    expect(c.r.status).toBe(429);
    expect(c.j.error).toBe("broadcast_full");
    expect(c.r.headers.get("Retry-After")).toBe("30");
    advanceClock(60_000);
    expect((await beatOf(s.pub, a.j.session_id as string)).r.status).toBe(200);
    expect((await beatOf(s.pub, b.j.session_id as string)).r.status).toBe(200);
    // wait mode (past the allowance and the fair-use ceiling): new viewers wait, playing ones go on
    await plan(s.pub, { mode: "wait", max_viewers: 100 });
    const w = await playWith(s, s.ch, pass);
    expect(w.r.status).toBe(429);
    expect(w.j.key).toBe("broadcast.waiting");
    const kept = await beatOf(s.pub, a.j.session_id as string);
    expect(kept.r.status).toBe(200);
    expect(kept.j.state).toBe("full");
    // off: refused
    await plan(s.pub, { mode: "off" });
    expect((await playWith(s, s.ch, pass)).r.status).toBe(403);
  });

  it("the channel's cap, the network's open sessions (a pass bypasses it), daily minutes", async () => {
    const s = await setup(
      {},
      {
        channels: {},
      }
    );
    const ch = newChannel();
    const open = newChannel();
    await access(s.pub, {
      open_allowed: true,
      max_sessions_per_ip: 2,
      daily_minutes: 2,
      channels: {
        [ch]: { access: "invite", max_viewers: 1, indexable: false },
        [open]: { access: "open", max_viewers: 0, indexable: false },
      },
    });
    const pass = await guest(s);
    expect((await playWith(s, ch, pass)).r.status).toBe(200);
    expect((await playWith(s, ch, await guest(s))).j.error).toBe("broadcast_full");
    // open sessions: 2 per network
    const sess = async (n: number) =>
      (
        (await (
          await post("/api/v1/open/turnstile", { token: `good-ip-${pubN}-${n}` })
        ).json()) as { session: string }
      ).session;
    const o = [await sess(1), await sess(2), await sess(3)];
    expect((await playWith(s, open, o[0]!, "100.64.0.1")).r.status).toBe(200);
    expect((await playWith(s, open, o[1]!, "100.64.0.1")).r.status).toBe(200);
    const third = await playWith(s, open, o[2]!, "100.64.0.1");
    expect(third.r.status).toBe(429);
    expect(third.j.error).toBe("broadcast_ip_limit");
    expect(third.j.key).toBe("broadcast.ipLimit");
    expect((await playWith(s, open, o[2]!, "100.64.0.2")).r.status).toBe(200);
    // a guest pass on that busy network still plays
    expect((await playWith(s, open, pass, "100.64.0.1")).r.status).toBe(200);
    // daily minutes: two counted minutes, then new plays wait for tomorrow
    const d = await guest(s);
    const p = await playWith(s, open, d, "100.64.0.9");
    for (let i = 0; i < 2; i++) {
      advanceClock(60_000);
      await beatOf(s.pub, p.j.session_id as string);
    }
    expect((await playWith(s, open, d, "100.64.0.9")).j.error).toBe("broadcast_daily_limit");
  });

  it("the per-address rate limit answers 429 before anything else", async () => {
    const s = await setup();
    const deny = { limit: async () => ({ success: false }) };
    const r = await post(
      "/api/v1/play",
      { publisher: s.pub, channel: s.ch },
      {},
      { PLAY_RL: deny }
    );
    expect(r.status).toBe(429);
    expect((await post("/api/v1/redeem", { invite: "x" }, {}, { API_RL: deny })).status).toBe(429);
  });
});

describe("T07 routes", () => {
  it("no payment route on watch. or live.", async () => {
    for (const r of WATCH_ROUTES)
      expect(r).not.toMatch(/pay|checkout|billing|purchase|subscri|price|card/i);
    for (const path of ["/api/v1/pay", "/api/v1/checkout", "/api/v1/billing", "/api/v1/purchase"]) {
      const w = await post(path, {});
      expect(w.status).toBe(404);
      const l = await call(`${LIVE}${path}`, { method: "POST" });
      expect(l.status).toBeGreaterThanOrEqual(400);
    }
  });
});

describe("T08 1,000 sessions", () => {
  it("heartbeats for 10 minutes: exact counts; viewer-minutes = beats", async () => {
    const s = await setup();
    await plan(s.pub, { max_viewers: 1000 });
    // 1,000 devices: two invites of 500
    const invites = [
      await invite({ pub: s.pub, max: 500 }),
      await invite({ pub: s.pub, max: 500 }),
    ];
    const passes: string[] = [];
    for (let i = 0; i < 1000; i += 100) {
      const batch = await Promise.all(
        Array.from({ length: 100 }, (_, k) => redeem(invites[(i + k) % 2]!))
      );
      for (const b of batch) passes.push(b.j.pass!);
    }
    const pass = await guest(s);
    const sessions: string[] = [];
    for (let i = 0; i < 1000; i += 100) {
      const batch = await Promise.all(passes.slice(i, i + 100).map((p) => playWith(s, s.ch, p)));
      for (const b of batch) {
        expect(b.r.status).toBe(200);
        sessions.push(b.j.session_id as string);
      }
    }
    const extra = await playWith(s, s.ch, pass);
    expect(extra.j.error).toBe("broadcast_full");
    const viewer = env.VIEWER.get(env.VIEWER.idFromName(s.pub));
    expect((await viewer.concurrent()).total).toBe(1000);
    let beats = 0;
    for (let m = 0; m < 10; m++) {
      advanceClock(60_000);
      for (let i = 0; i < 1000; i += 200) {
        const rs = await Promise.all(sessions.slice(i, i + 200).map((x) => beatOf(s.pub, x)));
        for (const b of rs) expect(b.r.status).toBe(200);
        beats += rs.length;
      }
    }
    expect((await viewer.concurrent()).total).toBe(1000);
    const usage = await env.PUBLISHER.get(env.PUBLISHER.idFromName(s.pub)).pendingUsage();
    const minutes = usage.reduce((n, w) => n + w.minutes_full + w.minutes_lowest, 0);
    expect(beats).toBe(10_000);
    expect(minutes).toBe(beats);
  });
});

describe("T09 strings", () => {
  it("every viewer message has English and Arabic", () => {
    for (const [key, v] of Object.entries(STRINGS)) {
      expect(v.en.trim(), key).not.toBe("");
      expect(v.ar, key).toMatch(/[؀-ۿ]/);
    }
    for (const key of Object.values(ERROR_KEYS)) expect(STRINGS[key], key).toBeDefined();
    for (const key of [
      "broadcast.invite.used",
      "broadcast.invite.expired",
      "broadcast.ipLimit",
      "broadcast.free",
      "broadcast.waiting",
      "broadcast.full",
    ])
      expect(STRINGS[key], key).toBeDefined();
  });

  it("answers in the asked language", async () => {
    const s = await setup();
    const en = await post(
      "/api/v1/play",
      { publisher: s.pub, channel: s.ch2 },
      { "Accept-Language": "en-GB,en;q=0.9" }
    );
    expect(((await en.json()) as { message?: string }).message).toBeUndefined(); // turnstile_required has no message
    const ar = await post(
      "/api/v1/play",
      { publisher: s.pub, channel: s.ch },
      { "Accept-Language": "ar" }
    );
    expect(((await ar.json()) as { message: string }).message).toBe(
      STRINGS["broadcast.inviteRequired"]!.ar
    );
  });
});

describe("the access control posts", () => {
  it("needs the cloud's signature and never goes back a version", async () => {
    const pub = newPub();
    const bad = await call(`${INGEST}/v1/control/access`, {
      method: "PUT",
      body: "mkbc1.e30.AAAA",
    });
    expect(bad.status).toBe(401);
    expect((await access(pub, { version: 50 })).status).toBe(200);
    expect((await access(pub, { version: 49 })).status).toBe(409);
    expect((await access(pub, { channels: "nope" })).status).toBe(400);
  });
});
