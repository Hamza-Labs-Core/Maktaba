// Story 50.25 at the edge: the signed revocation list (T02), what each action does to play,
// heartbeats, playlists and ingest (T03), the pull that backs up the push (T05), break-glass
// (T06) and a programme's slate that ends by itself (T07).
import { afterEach, describe, expect, it, vi } from "vitest";
import { runInDurableObject } from "cloudflare:test";
import { advanceClock, now, resetClock } from "../src/clock";
import type { Env } from "../src/env";
import { forgetRevocations, pullRevocations, verdictFor } from "../src/revocations";
import {
  call,
  E,
  INGEST,
  ingestToken,
  installList,
  LIVE,
  newChannel,
  putSegment,
  revocationList,
  uris,
  viewerToken,
} from "./helpers";

const revs = () => E.REVOCATIONS.get(E.REVOCATIONS.idFromName("global"));
const chan = (ch: string) => E.CHANNEL.get(E.CHANNEL.idFromName(ch));

afterEach(async () => {
  // leave an empty list for the other tests (the list is one object for the whole run)
  await installList([]);
  forgetRevocations();
  resetClock();
  vi.restoreAllMocks();
});

async function live(ch: string, pub: string, path: string): Promise<Response> {
  return call(`${LIVE}/${await viewerToken({ ch, pub })}/${ch}/${path}`, {
    method: path === "beat" ? "POST" : "GET",
  });
}

describe("T02 the Revocations object", () => {
  it("stores a newer list, refuses an older one, and takes an equal one as a no-op", async () => {
    const v = (await revs().current()).version;
    const ch = newChannel();
    const entry = { id: "r-1", kind: "channel", ref: ch, action: "off_air", reason: "notice" };
    const r = await call(`${INGEST}/v1/control/revocations`, {
      method: "PUT",
      body: await revocationList(v + 2, [entry]),
    });
    expect(r.status, await r.clone().text()).toBe(200);
    expect(await (await call(`${INGEST}/v1/control/revocations`)).json()).toEqual({
      version: v + 2,
    });
    const older = await call(`${INGEST}/v1/control/revocations`, {
      method: "PUT",
      body: await revocationList(v + 1, []),
    });
    expect(older.status).toBe(409);
    expect((await revs().current()).entries).toHaveLength(1);
    const equal = await call(`${INGEST}/v1/control/revocations`, {
      method: "PUT",
      body: await revocationList(v + 2, []),
    });
    expect(await equal.json()).toMatchObject({ ok: true, channels: 0 });
    expect((await revs().current()).entries).toHaveLength(1);
  });

  it("refuses a list with a bad signature and raises a SEV2 alert", async () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    const v = (await revs().current()).version;
    const good = await revocationList(v + 1, []);
    const tampered = good.slice(0, -4) + (good.endsWith("AAAA") ? "BBBB" : "AAAA");
    const r = await call(`${INGEST}/v1/control/revocations`, { method: "PUT", body: tampered });
    expect(r.status).toBe(401);
    // a key the edge does not hold: refused the same way
    const unknown = await revocationList(v + 1, [], "cloud-9");
    expect(
      (await call(`${INGEST}/v1/control/revocations`, { method: "PUT", body: unknown })).status
    ).toBe(401);
    expect((await revs().current()).version).toBe(v);
    const alerts = errors.mock.calls
      .map((c) => JSON.parse(String(c[0])) as { attrs?: { alert?: string } })
      .filter((l) => l.attrs?.alert === "SEV2");
    expect(alerts).toHaveLength(2);
  });

  it("refuses a list carrying an entry it may not (a slate with no end)", async () => {
    const v = (await revs().current()).version;
    const bad = await revocationList(v + 1, [
      { id: "r-1", kind: "programme", ref: "c", action: "slate", reason: "notice" },
    ]);
    const r = await call(`${INGEST}/v1/control/revocations`, { method: "PUT", body: bad });
    expect(r.status).toBe(400);
    expect(await r.json()).toEqual({ error: "bad_list" });
  });
});

describe("T03 each action at the edge", () => {
  it("off_air: play, the heartbeat and playlists answer 503 off_air; ingest 403 revoked; a later list restores it", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const token = await ingestToken({ ch, pub });
    expect((await putSegment(ch, "src", 1, { token })).status).toBe(201);
    // (no playlist read first: the edge caches one 1 s on the real clock, which the test can't move)

    expect(
      (
        await installList([
          { id: "r-1", kind: "channel", ref: ch, action: "off_air", reason: "notice" },
        ])
      ).status
    ).toBe(200);
    advanceClock(1500); // past the 1 s playlist cache: a playing viewer's next reload
    for (const path of ["master.m3u8", "src/index.m3u8", "beat"]) {
      const r = await live(ch, pub, path);
      expect(r.status, path).toBe(503);
      expect(await r.json()).toEqual({ error: "off_air", reason: "notice" });
    }
    const put = await putSegment(ch, "src", 2, { token });
    expect(put.status).toBe(403);
    expect(await put.json()).toEqual({ error: "revoked", reason: "notice" });
    const snap = await runInDurableObject(chan(ch), (o) => o.snapshot());
    expect(snap.status).toBe("revoked");

    // only a later version without the entry lifts it
    await installList([]);
    advanceClock(1500);
    expect((await putSegment(ch, "src", 2, { token })).status).toBe(201);
    expect((await live(ch, pub, "master.m3u8")).status).toBe(200);
  });

  it("suspend and terminate on a publisher reach every channel of the publisher", async () => {
    for (const action of ["suspend", "terminate"]) {
      const [a, b] = [newChannel(), newChannel()];
      const pub = `pub-${a}`;
      await putSegment(a, "src", 1, { token: await ingestToken({ ch: a, pub }) });
      await putSegment(b, "src", 1, { token: await ingestToken({ ch: b, pub }) });
      const r = await installList([
        { id: "r-2", kind: "publisher", ref: pub, action, reason: "repeat_infringer" },
      ]);
      expect(await r.json()).toMatchObject({ ok: true, channels: 2 });
      advanceClock(1500);
      for (const ch of [a, b]) {
        const snap = await runInDurableObject(chan(ch), (o) => o.snapshot());
        expect(snap.verdict).toMatchObject({ stop: "off_air", action });
        expect((await live(ch, pub, "master.m3u8")).status).toBe(503);
        const put = await putSegment(ch, "src", 2, { token: await ingestToken({ ch, pub }) });
        expect(put.status).toBe(403);
      }
      await installList([]);
    }
  });

  it("a home's suspension stops its channels within the 10 s check, with no fan-out", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const home = `home-${ch}`;
    const token = await ingestToken({ ch, pub, home });
    expect((await putSegment(ch, "src", 1, { token })).status).toBe(201);
    const r = await installList([
      { id: "r-3", kind: "home", ref: home, action: "suspend", reason: "abuse" },
    ]);
    expect(await r.json()).toMatchObject({ ok: true, channels: 0 });
    // ingest asks the list itself (the token names the home)
    expect((await putSegment(ch, "src", 2, { token })).status).toBe(403);
    advanceClock(10_500);
    const p = await live(ch, pub, "src/index.m3u8");
    expect(p.status).toBe(503);
    expect(await p.json()).toEqual({ error: "off_air", reason: "abuse" });
  });

  it("a global entry stops every channel", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    await putSegment(ch, "src", 1, { token: await ingestToken({ ch, pub }) });
    await installList([{ id: "r-4", kind: "global", action: "off_air", reason: "incident" }]);
    advanceClock(10_500);
    const p = await live(ch, pub, "master.m3u8");
    expect(p.status).toBe(503);
    expect(await p.json()).toEqual({ error: "off_air", reason: "incident" });
  });

  it("tells the home over the control socket: revoked, then restored", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const token = await ingestToken({ ch, pub });
    await putSegment(ch, "src", 1, { token });
    const res = await call(`${INGEST}/v1/ingest/ws`, {
      headers: {
        Upgrade: "websocket",
        Authorization: `Bearer ${token}`,
        "X-Maktaba-Time": String(now()),
      },
    });
    const ws = res.webSocket!;
    const got: Record<string, unknown>[] = [];
    ws.addEventListener("message", (e) => got.push(JSON.parse(String(e.data))));
    ws.accept();
    await installList([
      { id: "r-5", kind: "channel", ref: ch, action: "off_air", reason: "report" },
    ]);
    await installList([]);
    await vi.waitFor(() => expect(got.map((m) => m.type)).toContain("restored"));
    const revoked = got.find((m) => m.type === "revoked");
    expect(revoked).toMatchObject({ action: "off_air", reason: "report" });
    ws.close();
  });
});

describe("T07 a programme's slate", () => {
  it("loops the slate in every rung's playlist until `until`, keeps ingest open, then returns to live by itself", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const token = await ingestToken({ ch, pub });
    for (const idx of [0, 1])
      expect((await putSegment(ch, "slate", idx, { token, salt: "slate" })).status).toBe(201);
    expect((await putSegment(ch, "src", 1, { token })).status).toBe(201);
    const until = new Date(now() + 60_000).toISOString();
    await installList([
      { id: "r-6", kind: "programme", ref: ch, action: "slate", reason: "notice", until },
    ]);
    advanceClock(1500);
    const during = await (await live(ch, pub, "src/index.m3u8")).text();
    expect(during).toContain("../slate/");
    expect(during).not.toMatch(/^1-[0-9a-f]{8}\.ts$/m);
    // the home keeps uploading (the takedown comes separately, 50.24)
    expect((await putSegment(ch, "src", 2, { token })).status).toBe(201);
    // 21:00: live again with no one acting
    advanceClock(60_000);
    await putSegment(ch, "src", 3, { token });
    // the slate loop's 1 s edge-cache entry ages out on the real clock
    await new Promise((r) => setTimeout(r, 1100));
    const after = uris(await (await live(ch, pub, "src/index.m3u8")).text());
    // the newest entry is the live segment (the window may still hold the ordinary offline
    // slate of the minute without uploads)
    expect(after.at(-1)).toMatch(/^3-[0-9a-f]{8}\.ts$/);
  });

  it("is off air while the slate holds if the channel uploaded none", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    await putSegment(ch, "src", 1, { token: await ingestToken({ ch, pub }) });
    await installList([
      {
        id: "r-7",
        kind: "programme",
        ref: ch,
        action: "slate",
        reason: "notice",
        until: new Date(now() + 60_000).toISOString(),
      },
    ]);
    advanceClock(1500);
    expect((await live(ch, pub, "src/index.m3u8")).status).toBe(503);
  });

  it("verdictFor: an off_air beats a slate, and a slate past its until is nothing", () => {
    const t = Date.parse("2026-11-03T20:00:00Z");
    const list = {
      version: 1,
      entries: [
        {
          id: "a",
          kind: "programme" as const,
          ref: "c",
          action: "slate" as const,
          reason: "notice",
          until: "2026-11-03T21:00:00Z",
        },
      ],
    };
    expect(verdictFor(list, { ch: "c" }, t)).toMatchObject({ stop: "slate" });
    expect(verdictFor(list, { ch: "c" }, Date.parse("2026-11-03T21:00:00Z"))).toBeNull();
    list.entries.push({
      id: "b",
      kind: "publisher" as never,
      ref: "p",
      action: "suspend" as never,
      reason: "abuse",
      until: undefined as never,
    });
    expect(verdictFor(list, { ch: "c", pub: "p" }, t)).toMatchObject({
      stop: "off_air",
      action: "suspend",
    });
  });
});

describe("T06 break-glass", () => {
  it("BROADCAST_OFF_CHANNELS takes one channel off air at once; clearing it brings it back", async () => {
    const [ch, other] = [newChannel(), newChannel()];
    const pub = `pub-${ch}`;
    const token = await ingestToken({ ch, pub });
    await putSegment(ch, "src", 1, { token });
    await putSegment(other, "src", 1, { token: await ingestToken({ ch: other, pub }) });
    const glass: Partial<Env> = { BROADCAST_OFF_CHANNELS: ` ${ch} ,chX` };
    const tok = await viewerToken({ ch, pub });
    const r = await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`, undefined, glass);
    expect(r.status).toBe(503);
    expect(await r.json()).toEqual({ error: "off_air", reason: "incident" });
    const put = await call(
      `${INGEST}/v1/ingest/${ch}/state`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "X-Maktaba-Time": String(now()) },
        body: "{}",
      },
      glass
    );
    expect(put.status).toBe(403);
    const otherTok = await viewerToken({ ch: other, pub });
    expect(
      (await call(`${LIVE}/${otherTok}/${other}/src/index.m3u8`, undefined, glass)).status
    ).toBe(200);
    // cleared by hand when the API is back
    expect((await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`)).status).toBe(200);
  });

  it("BROADCAST_GLOBAL_OFF = 1 is off air for every channel, before the viewer token is read", async () => {
    const ch = newChannel();
    const r = await call(`${LIVE}/not-a-token/${ch}/master.m3u8`, undefined, {
      BROADCAST_GLOBAL_OFF: "1",
    });
    expect(r.status).toBe(503);
    expect(await r.json()).toEqual({ error: "off_air", reason: "incident" });
  });
});

describe("T05 the pull backs up the push", () => {
  it("asks the API with the edge's signature and installs the list it gets", async () => {
    const V = JSON.parse(E.TEST_VECTORS) as { edge: { seed: string } };
    const ch = newChannel();
    const v = (await revs().current()).version;
    const body = await revocationList(v + 1, [
      { id: "r-8", kind: "channel", ref: ch, action: "off_air", reason: "notice" },
    ]);
    const asked: Request[] = [];
    const fetcher = (async (input: RequestInfo | URL, init?: RequestInit) => {
      asked.push(new Request(input, init));
      return new Response(body, { status: 200 });
    }) as typeof fetch;
    const env = { ...E, API_ORIGIN: "https://api.example.test", EDGE_SIGNING_KEY: V.edge.seed };
    const r = await pullRevocations(env, fetcher);
    expect(r).toMatchObject({ ok: true, version: v + 1 });
    expect(asked[0]!.url).toBe("https://api.example.test/v1/broadcast/revocations");
    expect(asked[0]!.headers.get("X-Maktaba-Edge-Signature")).toBeTruthy();
    expect((await revs().current()).version).toBe(v + 1);
    // with no API origin, nothing is asked
    expect(await pullRevocations({ ...E, API_ORIGIN: undefined }, fetcher)).toBeNull();
    expect(asked).toHaveLength(1);
  });
});
