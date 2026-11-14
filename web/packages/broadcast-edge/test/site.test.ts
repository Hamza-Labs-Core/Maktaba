// Story 50.27: guest viewers on watch.maktaba.tv. The guide a home publishes, the watch pages
// (grid, channel, guide, legal, the language), T01 the guest states and their words, T07 the
// no-directory rule, T08 every new string in English and Arabic, the apps' lineup and guide
// reads, and TV pairing (the QR flow's long-poll).
import { afterEach, describe, expect, it } from "vitest";
import { advanceClock, now, resetClock } from "../src/clock";
import type { Env } from "../src/env";
import { MAX_GUIDE_BYTES, nowNext, validGuide } from "../src/guide";
import { INVITE_DOMAIN, INVITE_PREFIX, type InviteClaims } from "../src/invites";
import { WATCH_ROUTES } from "../src/play";
import { forgetRevocations } from "../src/revocations";
import { clientJS } from "../src/site/client";
import { inviteToken } from "../src/site/routes";
import { viewerState } from "../src/site/states";
import { ERROR_KEYS, STRINGS } from "../src/strings";
import { call, cloudKid, control, INGEST, ingestToken, newChannel, signCloud } from "./helpers";

afterEach(() => {
  resetClock();
  forgetRevocations();
});

const WATCH = "https://watch.maktaba.tv";
let n = 0;
let version = 1_000_000;

function newPub(): string {
  n++;
  return `5027aaaa-1111-4222-8333-${(Date.now() % 1e9).toString().padStart(9, "0")}${String(n).padStart(3, "0")}`;
}

async function invite(over: Partial<InviteClaims> & { pub: string }): Promise<string> {
  const t = Math.floor(now() / 1000);
  return signCloud(INVITE_PREFIX, INVITE_DOMAIN, {
    typ: "invite",
    chs: [],
    inv: `inv-${Math.random().toString(36).slice(2)}`,
    max: 10,
    iat: t,
    exp: t + 30 * 86400,
    kid: await cloudKid(),
    ...over,
  });
}

interface Setup {
  pub: string;
  ch: string;
  ch2: string;
}

async function access(pub: string, over: Record<string, unknown> = {}): Promise<void> {
  const r = await call(`${INGEST}/v1/control/access`, {
    method: "PUT",
    body: await control("access", {
      pub,
      version: version++,
      channels: {},
      open_allowed: true,
      max_sessions_per_ip: 3,
      daily_minutes: 0,
      revoked_invites: [],
      revoked_passes: [],
      codes: {},
      ...over,
    }),
  });
  expect(r.status).toBe(200);
}

async function plan(pub: string): Promise<void> {
  const r = await call(`${INGEST}/v1/control/plan`, {
    method: "PUT",
    body: await control("plan", { pub, mode: "full", version: version++, max_viewers: 100 }),
  });
  expect(r.status).toBe(200);
}

function iso(ms: number): string {
  return new Date(ms).toISOString();
}

function guideDoc(name: string, number: number, over: Record<string, unknown> = {}) {
  const t = now();
  return {
    schema: 1,
    publisher: { name: "Cairo Family TV", logo: "data:image/png;base64,iVBORw0KGgo=" },
    channel: { name, number },
    programmes: [
      { start: iso(t - 30 * 60_000), end: iso(t + 30 * 60_000), title: `${name} now` },
      {
        start: iso(t + 30 * 60_000),
        end: iso(t + 90 * 60_000),
        title: `${name} next`,
        attribution: {
          title: "A Short Film",
          by: "Jane Maker",
          licence: "CC BY 4.0",
          url: "https://example.org/film",
        },
      },
    ],
    ...over,
  };
}

async function postGuide(pub: string, ch: string, doc: unknown): Promise<Response> {
  const body = typeof doc === "string" ? doc : JSON.stringify(doc);
  return call(`${INGEST}/v1/ingest/${ch}/guide`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${await ingestToken({ ch, pub })}`,
      "X-Maktaba-Time": String(now()),
      "Content-Type": "application/json",
    },
    body,
  });
}

/** setup: a publisher with an invite-only channel (ch) and an open one (ch2), both with guides. */
async function setup(): Promise<Setup> {
  const pub = newPub();
  const [ch, ch2] = [newChannel(), newChannel()];
  await access(pub, {
    channels: {
      [ch]: { access: "invite", max_viewers: 0, indexable: false },
      [ch2]: { access: "open", max_viewers: 0, indexable: true },
    },
  });
  await plan(pub);
  expect((await postGuide(pub, ch, guideDoc("Family", 1))).status).toBe(200);
  expect((await postGuide(pub, ch2, guideDoc("Cartoons", 2))).status).toBe(200);
  return { pub, ch, ch2 };
}

async function passFor(s: Setup, over: Partial<InviteClaims> = {}): Promise<string> {
  const r = await call(`${WATCH}/api/v1/redeem`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ invite: await invite({ pub: s.pub, ...over }) }),
  });
  expect(r.status).toBe(200);
  return ((await r.json()) as { pass: string }).pass;
}

function get(path: string, headers: Record<string, string> = {}, envOver: Partial<Env> = {}) {
  return call(`${WATCH}${path}`, { headers }, envOver);
}

function postJSON(path: string, body: unknown, headers: Record<string, string> = {}) {
  return call(`${WATCH}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.9", ...headers },
    body: JSON.stringify(body),
  });
}

/** links lists every href, src and action in a page. */
function links(html: string): string[] {
  return [...html.matchAll(/\s(?:href|src|action)="([^"]*)"/g)].map((m) =>
    m[1]!.replace(/&#38;/g, "&")
  );
}

describe("the guide a home publishes", () => {
  it("stores a valid guide; refuses a bad schema, a bad logo, a bad programme and a big body", async () => {
    const pub = newPub();
    const ch = newChannel();
    const ok = await postGuide(pub, ch, guideDoc("News", 4));
    expect(ok.status).toBe(200);
    expect(((await ok.json()) as { programmes: number }).programmes).toBe(2);
    expect((await postGuide(pub, ch, guideDoc("News", 4, { schema: 2 }))).status).toBe(400);
    const badLogo = guideDoc("News", 4);
    badLogo.publisher.logo = "https://elsewhere.example/logo.png";
    expect((await postGuide(pub, ch, badLogo)).status).toBe(400);
    const badProg = guideDoc("News", 4, {
      programmes: [{ start: iso(now()), end: iso(now() - 1), title: "backwards" }],
    });
    expect((await postGuide(pub, ch, badProg)).status).toBe(400);
    expect((await postGuide(pub, ch, "x".repeat(MAX_GUIDE_BYTES + 1))).status).toBe(413);
    // only with the channel's own ingest token
    const other = await call(`${INGEST}/v1/ingest/${ch}/guide`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${await ingestToken({ ch: newChannel(), pub })}`,
        "X-Maktaba-Time": String(now()),
      },
      body: JSON.stringify(guideDoc("News", 4)),
    });
    expect(other.status).toBeGreaterThanOrEqual(401);
  });

  it("keeps the home's order of nothing: sorted by start, cut to the viewers' window", () => {
    const t = Date.UTC(2026, 10, 14, 12);
    const doc = validGuide(
      {
        schema: 1,
        publisher: { name: "P" },
        channel: { name: "C", number: 1 },
        programmes: [
          { start: iso(t + 3600e3), end: iso(t + 7200e3), title: "later" },
          { start: iso(t - 3 * 86400e3), end: iso(t - 2 * 86400e3), title: "long gone" },
          { start: iso(t - 600e3), end: iso(t + 3600e3), title: "on now" },
        ],
      },
      t
    );
    expect(typeof doc).toBe("object");
    if (typeof doc === "string") return;
    expect(doc.programmes.map((p) => p.title)).toEqual(["on now", "later"]);
    const nn = nowNext(doc.programmes, t);
    expect(nn.now?.title).toBe("on now");
    expect(nn.next?.title).toBe("later");
  });
});

describe("T01 the guest states and their words", () => {
  it("play, a 429, a 451, an ended channel, the channel's own states", () => {
    expect(viewerState(200, { channel_state: "live" })).toMatchObject({
      state: "live",
      plays: true,
    });
    expect(viewerState(200, { channel_state: "waiting" })).toMatchObject({
      state: "tuning",
      key: "broadcast.tuning",
      plays: true,
    });
    expect(viewerState(200, { channel_state: "offline_slate" })).toMatchObject({
      state: "offline",
      key: "broadcast.homeOffline",
      // the slate plays and the note shows: no error
      plays: true,
    });
    expect(viewerState(200, { channel_state: "off_air" })).toMatchObject({
      state: "off_air",
      retry: true,
    });
    expect(viewerState(429, { error: "broadcast_full" })).toMatchObject({
      state: "full",
      key: "broadcast.full",
      retry: true,
    });
    expect(viewerState(429, { error: "broadcast_ip_limit" }).key).toBe("broadcast.ipLimit");
    expect(viewerState(451, {})).toMatchObject({
      state: "geoBlocked",
      key: "broadcast.geoBlocked",
    });
    expect(viewerState(403, { error: "broadcast_off" })).toMatchObject({
      state: "ended",
      key: "broadcast.ended",
    });
    expect(viewerState(404, { error: "channel_unknown" }).state).toBe("ended");
    expect(viewerState(410, { error: "invite_used" }).state).toBe("inviteUsed");
    expect(viewerState(410, { error: "invite_expired" }).state).toBe("inviteExpired");
    expect(viewerState(410, { error: "revoked" }).state).toBe("revoked");
    expect(viewerState(403, { error: "turnstile_required" }).state).toBe("verify");
    expect(viewerState(500, null)).toMatchObject({ state: "error", retry: true });
  });

  it("every state's words exist in English and Arabic", () => {
    const cases: [number, Record<string, string>][] = [
      [200, { channel_state: "waiting" }],
      [200, { channel_state: "offline_slate" }],
      [200, { channel_state: "off_air" }],
      [200, { channel_state: "revoked" }],
      [451, {}],
      [500, {}],
      ...Object.keys(ERROR_KEYS).map((e): [number, Record<string, string>] => [400, { error: e }]),
      [403, { error: "turnstile_required" }],
    ];
    for (const [status, b] of cases) {
      const { key } = viewerState(status, b);
      expect(STRINGS[key]?.en, key).toBeTruthy();
      expect(STRINGS[key]?.ar, key).toBeTruthy();
    }
  });

  it("the page script carries the same mapping, self-contained", () => {
    const js = clientJS();
    expect(js).toContain("var __name=");
    expect(js).toContain("broadcast.homeOffline");
    expect(js).not.toMatch(/\bimport\b|\brequire\(/);
  });
});

describe("T08 strings", () => {
  it("every key has English and Arabic, and the Arabic is Arabic", () => {
    for (const [key, v] of Object.entries(STRINGS)) {
      expect(v.en, key).toBeTruthy();
      expect(v.ar, key).toBeTruthy();
      expect(v.ar, key).toMatch(/[؀-ۿ]/);
    }
    for (const key of [
      "guest.watchBroadcast",
      "guest.enterCode",
      "guest.scanQr",
      "guest.saved",
      "guest.remove",
      "guest.onMaktaba",
      "broadcast.homeOffline",
      "broadcast.ended",
      "broadcast.tuning",
      "guest.tryAgain",
    ])
      expect(STRINGS[key], key).toBeDefined();
    expect(STRINGS["guest.onMaktaba"]!.ar).toBe("{publisher} على مكتبة");
  });
});

describe("the watch pages", () => {
  it("an invite opened on a phone: redeemed, then the grid in Arabic, right to left", async () => {
    const s = await setup();
    const tok = await invite({ pub: s.pub });
    const r = await call(`${WATCH}/i/${tok}`, { method: "POST" });
    expect(r.status).toBe(303);
    expect(r.headers.get("Location")).toBe(`/ar/${s.pub}`);
    const cookie = r.headers.get("Set-Cookie")!.split(";")[0]!;
    const grid = await get(`/ar/${s.pub}`, { Cookie: cookie });
    expect(grid.status).toBe(200);
    const html = await grid.text();
    expect(html).toContain('dir="rtl"');
    expect(html).toContain("Cairo Family TV على مكتبة");
    expect(html).toContain("Family");
    expect(html).toContain("Cartoons");
    expect(html).toContain("Family now");
    expect(html).toContain("المشاهدة مجانية");
    expect(grid.headers.get("Content-Security-Policy")).toContain("default-src 'none'");
    expect(grid.headers.get("X-Robots-Tag")).toBe("noindex");
  });

  it("an invite for one channel goes straight to it", async () => {
    const s = await setup();
    const tok = await invite({ pub: s.pub, chs: [s.ch] });
    const r = await call(`${WATCH}/i/${tok}`, {
      method: "POST",
      headers: { "Accept-Language": "en" },
    });
    expect(r.headers.get("Location")).toBe(`/en/${s.pub}/${s.ch}`);
  });

  it("the channel page: the player, now and next, today, attribution, Report", async () => {
    const s = await setup();
    const pass = await passFor(s);
    const r = await get(`/en/${s.pub}/${s.ch}`, { Cookie: `mkp_${s.pub}=${pass}` });
    expect(r.status).toBe(200);
    const html = await r.text();
    expect(html).toContain('dir="ltr"');
    expect(html).toContain('<video id="video"');
    expect(html).toContain("Now: Family now");
    expect(html).toContain("Family next");
    // the CC BY programme's credit, with its link
    expect(html).toContain("A Short Film by Jane Maker, CC BY 4.0");
    expect(html).toContain('href="https://example.org/film"');
    expect(html).toContain(">Report<");
    expect(html).toContain('id="cfg"');
    expect(html).toContain("/s/watch.js");
    // a channel of the publisher's that is not indexable stays noindex
    expect(r.headers.get("X-Robots-Tag")).toBe("noindex");
    expect(html).toContain('content="noindex"');
  });

  it("an open channel the publisher lets search engines index drops noindex", async () => {
    const s = await setup();
    const r = await get(`/en/${s.pub}/${s.ch2}`);
    expect(r.status).toBe(200);
    expect(r.headers.get("X-Robots-Tag")).toBeNull();
    expect(await r.text()).not.toContain('content="noindex"');
  });

  it("without a credential only open channels show; a pass for one channel shows that one", async () => {
    const s = await setup();
    const anon = await (await get(`/en/${s.pub}`)).text();
    expect(anon).toContain("Cartoons");
    expect(anon).not.toContain(">Family<");
    expect((await get(`/en/${s.pub}/${s.ch}`)).status).toBe(404);
    const one = await passFor(s, { chs: [s.ch] });
    const cookie = { Cookie: `mkp_${s.pub}=${one}` };
    const html = await (await get(`/en/${s.pub}`, cookie)).text();
    expect(html).toContain("Family");
    expect(html).not.toContain("Cartoons");
    expect((await get(`/en/${s.pub}/${s.ch2}`, cookie)).status).toBe(404);
  });

  it("the guide page lists today's programmes per channel", async () => {
    const s = await setup();
    const pass = await passFor(s);
    const html = await (await get(`/en/${s.pub}/guide`, { Cookie: `mkp_${s.pub}=${pass}` })).text();
    expect(html).toContain("Family now");
    expect(html).toContain("Cartoons next");
  });

  it("the language: the prefix decides, otherwise Accept-Language (Arabic by default)", async () => {
    const s = await setup();
    const r = await get(`/${s.pub}`, { "Accept-Language": "en-GB,en;q=0.9" });
    expect(r.status).toBe(302);
    expect(r.headers.get("Location")).toBe(`/en/${s.pub}`);
    expect((await get(`/${s.pub}/guide`)).headers.get("Location")).toBe(`/ar/${s.pub}/guide`);
    const home = await (await get("/", { "Accept-Language": "en" })).text();
    expect(home).toContain("invite link");
  });

  it("legal pages in both languages, and the static script and style", async () => {
    for (const p of ["copyright", "terms", "privacy"]) {
      expect((await get(`/en/legal/${p}`)).status).toBe(200);
      const ar = await (await get(`/ar/legal/${p}`)).text();
      expect(ar).toContain(`https://maktaba.tv/ar/legal/${p}/`);
    }
    const js = await get("/s/watch.js");
    expect(js.headers.get("Content-Type")).toContain("javascript");
    const css = await get("/s/watch.css");
    expect(css.headers.get("Content-Type")).toContain("text/css");
  });

  it("WATCH_SITE_OFF: the pages say temporarily unavailable; the API still answers", async () => {
    const s = await setup();
    const off = await get(`/en/${s.pub}`, {}, { WATCH_SITE_OFF: "1" });
    expect(off.status).toBe(503);
    expect(await off.text()).toContain("temporarily unavailable");
    const api = await get(`/api/v1/lineup?publisher=${s.pub}`, {}, { WATCH_SITE_OFF: "1" });
    expect(api.status).toBe(200);
  });

  it("T07 no directory: a publisher's pages link to no other publisher", async () => {
    const a = await setup();
    const b = await setup();
    const pass = await passFor(a);
    const cookie = { Cookie: `mkp_${a.pub}=${pass}; mkp_${b.pub}=${await passFor(b)}` };
    const allowed = [
      new RegExp(`^/(ar|en)/${a.pub}(/|$)`),
      /^\/(ar|en)\/legal\/(copyright|terms|privacy)$/,
      /^\/s\/watch\.(js|css)$/,
      /^https:\/\/cdn\.jsdelivr\.net\/npm\/hls\.js@/,
      /^https:\/\/challenges\.cloudflare\.com\//,
      /^https:\/\/example\.org\/film$/, // the CC BY credit's source
      /^data:image\//, // the publisher's own logo
    ];
    for (const path of [
      `/ar/${a.pub}`,
      `/en/${a.pub}/${a.ch}`,
      `/en/${a.pub}/${a.ch2}`,
      `/ar/${a.pub}/guide`,
    ]) {
      const html = await (await get(path, cookie)).text();
      expect(html).not.toContain(b.pub);
      for (const l of links(html))
        expect(
          allowed.some((re) => re.test(l)),
          `${path} links to ${l}`
        ).toBe(true);
    }
  });

  it("the routes take no payment (50.26 T07 still holds)", () => {
    for (const r of WATCH_ROUTES)
      expect(r).not.toMatch(/pay|checkout|billing|purchase|subscri|price|card/i);
  });
});

describe("the apps' reads", () => {
  it("lineup and guide with a pass in the keychain (Bearer)", async () => {
    const s = await setup();
    const pass = await passFor(s, { chs: [s.ch] });
    const auth = { Authorization: `Bearer ${pass}` };
    const l = (await (await get(`/api/v1/lineup?publisher=${s.pub}`, auth)).json()) as {
      publisher: { name: string; logo?: string };
      channels: { id: string; name: string; number: number; now: { title: string } | null }[];
    };
    expect(l.publisher.name).toBe("Cairo Family TV");
    expect(l.publisher.logo).toMatch(/^data:image\/png/);
    expect(l.channels.map((c) => c.id)).toEqual([s.ch]);
    expect(l.channels[0]!.now?.title).toBe("Family now");
    const g = await get(`/api/v1/guide?publisher=${s.pub}&channel=${s.ch}`, auth);
    expect(g.status).toBe(200);
    expect(((await g.json()) as { programmes: unknown[] }).programmes).toHaveLength(2);
    // a channel the pass doesn't open
    expect((await get(`/api/v1/guide?publisher=${s.pub}&channel=${s.ch2}`, auth)).status).toBe(403);
  });

  it("the play answer carries the channel's own state", async () => {
    const s = await setup();
    const pass = await passFor(s);
    const r = await postJSON(
      "/api/v1/play",
      { publisher: s.pub, channel: s.ch },
      { Authorization: `Bearer ${pass}` }
    );
    expect(r.status).toBe(200);
    const j = (await r.json()) as { channel_state: string };
    // no segment yet, the home hasn't said it is on air
    expect(["waiting", "off_air"]).toContain(j.channel_state);
  });
});

describe("TV pairing", () => {
  async function open(): Promise<{ nonce: string; url: string }> {
    const r = await postJSON("/api/v1/pair", {});
    expect(r.status).toBe(201);
    return (await r.json()) as { nonce: string; url: string };
  }

  it("the QR flow: the phone sends an invite, the TV's long-poll gets a pass that plays", async () => {
    const s = await setup();
    const p = await open();
    expect(p.url).toBe(`${WATCH}/pair?tv=${p.nonce}`);
    // nothing yet
    expect((await get(`/api/v1/pair/${p.nonce}?wait=0`)).status).toBe(204);
    // the TV waits; the phone sends while it waits
    const waiting = get(`/api/v1/pair/${p.nonce}?wait=10`);
    const inv = await invite({ pub: s.pub, max: 1 });
    const sent = await postJSON(`/api/v1/pair/${p.nonce}`, { invite: `${WATCH}/i/${inv}` });
    expect(sent.status).toBe(200);
    const got = await waiting;
    expect(got.status).toBe(200);
    const h = (await got.json()) as { credential: string; kind: string; publisher: string };
    expect(h.kind).toBe("pass");
    expect(h.publisher).toBe(s.pub);
    expect(h.credential).toMatch(/^mkbg1\./);
    // handed over once
    expect((await get(`/api/v1/pair/${p.nonce}?wait=0`)).status).toBe(410);
    // the TV's pass plays
    const play = await postJSON(
      "/api/v1/play",
      { publisher: s.pub, channel: s.ch },
      { Authorization: `Bearer ${h.credential}` }
    );
    expect(play.status).toBe(200);
    // the TV counted one device use: the 1-use invite is now used up
    const again = await postJSON("/api/v1/redeem", { invite: inv });
    expect(again.status).toBe(410);
  });

  it("the phone can send a short code with the publisher", async () => {
    const s = await setup();
    const inv = await invite({ pub: s.pub });
    await access(s.pub, {
      channels: { [s.ch]: { access: "invite", max_viewers: 0, indexable: false } },
      codes: { "MK-AB12": inv },
    });
    const p = await open();
    const sent = await postJSON(`/api/v1/pair/${p.nonce}`, { publisher: s.pub, code: "mk-ab12" });
    expect(sent.status).toBe(200);
    expect((await get(`/api/v1/pair/${p.nonce}?wait=0`)).status).toBe(200);
  });

  it("the phone's open session gives the TV its own", async () => {
    const s = await setup();
    const ts = await postJSON("/api/v1/open/turnstile", { token: `good-${Math.random()}` });
    expect(ts.status).toBe(200);
    const session = ((await ts.json()) as { session: string }).session;
    const p = await open();
    const refused = await postJSON(`/api/v1/pair/${p.nonce}`, { publisher: s.pub, open: true });
    expect(refused.status).toBe(403);
    const sent = await postJSON(
      `/api/v1/pair/${p.nonce}`,
      { publisher: s.pub, open: true },
      { Cookie: `mko=${session}` }
    );
    expect(sent.status).toBe(200);
    const h = (await (await get(`/api/v1/pair/${p.nonce}?wait=0`)).json()) as {
      credential: string;
      kind: string;
      channels: string[];
    };
    expect(h.kind).toBe("open");
    expect(h.credential).not.toBe(session);
    expect(h.channels).toEqual([s.ch2]);
  });

  it("a pairing ends after 10 minutes; a wrong nonce is gone", async () => {
    const s = await setup();
    const p = await open();
    advanceClock(10 * 60_000 + 1);
    const late = await postJSON(`/api/v1/pair/${p.nonce}`, {
      invite: await invite({ pub: s.pub }),
    });
    expect(late.status).toBe(410);
    expect(((await late.json()) as { key: string }).key).toBe("site.pair.gone");
    expect((await get(`/api/v1/pair/${p.nonce}?wait=0`)).status).toBe(410);
    expect((await get(`/api/v1/pair/nope?wait=0`)).status).toBe(410);
  });

  it("the phone's page: a form that sends, with no script", async () => {
    const s = await setup();
    const p = await open();
    const pageHtml = await (await get(`/pair?tv=${p.nonce}`, { "Accept-Language": "en" })).text();
    expect(pageHtml).toContain("Paste the invite link");
    const inv = await invite({ pub: s.pub });
    const form = new FormData();
    form.set("invite", `${WATCH}/i/${inv}`);
    const r = await call(`${WATCH}/en/pair?tv=${p.nonce}`, { method: "POST", body: form });
    expect(r.status).toBe(200);
    expect(await r.text()).toContain("The TV will start in a moment");
    expect((await get(`/api/v1/pair/${p.nonce}?wait=0`)).status).toBe(200);
  });

  it("an invite link or a bare token", () => {
    expect(inviteToken("https://watch.maktaba.tv/i/mkbn1.abc.def?x=1")).toBe("mkbn1.abc.def");
    expect(inviteToken(" mkbn1.abc.def ")).toBe("mkbn1.abc.def");
  });
});
