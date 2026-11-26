// Broadcasting is off (BROADCAST_ENABLED unset or anything but "1" and "true"): the edge takes and
// serves nothing, whoever asks and whatever they hold. The revocation list alone still arrives,
// pushed by the cloud or pulled by the cron.
import {
  createExecutionContext,
  createScheduledController,
  waitOnExecutionContext,
} from "cloudflare:test";
import { afterEach, describe, expect, it, vi } from "vitest";
import { advanceClock, now, resetClock } from "../src/clock";
import type { Env } from "../src/env";
import { forgetRevocations } from "../src/revocations";
import worker, { broadcastingOn } from "../src/worker";
import {
  call,
  control,
  E,
  INGEST,
  ingestToken,
  installList,
  LIVE,
  newChannel,
  postState,
  putSegment,
  revocationList,
  segmentBody,
  STATE_3,
  viewerToken,
} from "./helpers";

const WATCH = "https://watch.maktaba.tv";
const OFF: Partial<Env> = { BROADCAST_ENABLED: "0" };
const revs = () => E.REVOCATIONS.get(E.REVOCATIONS.idFromName("global"));

afterEach(async () => {
  await installList([]);
  forgetRevocations();
  resetClock();
  vi.restoreAllMocks();
});

async function refused(r: Response, what: string): Promise<void> {
  expect(r.status, what).toBe(404);
  expect(await r.text(), what).toBe("Not found\n");
  expect(r.headers.get("Cache-Control"), what).toBe("no-store");
  expect(r.headers.get("X-Robots-Tag"), what).toContain("noindex");
  expect(r.headers.get("Set-Cookie"), what).toBeNull();
  expect(r.webSocket ?? null, what).toBeNull();
}

/** Every key the bucket holds. */
async function stored(b: R2Bucket | undefined): Promise<string[]> {
  return (await b!.list()).objects.map((o) => o.key).sort();
}

/** A channel on the air, with a segment, a state and a guide, from before the switch. */
async function onAir(): Promise<{ ch: string; token: string; view: string }> {
  const ch = newChannel();
  const token = await ingestToken({ ch });
  expect((await postState(ch, STATE_3, token)).status).toBe(200);
  for (const rung of ["src", "720p", "480p"])
    for (let seq = 1; seq <= 4; seq++) {
      advanceClock(1000);
      expect((await putSegment(ch, rung, seq, { token })).status, `${rung}/${seq}`).toBe(201);
    }
  return { ch, token, view: await viewerToken({ ch }) };
}

describe("broadcasting off", () => {
  it("is off unless the setting is exactly on", () => {
    for (const [v, on] of [
      [undefined, false],
      ["", false],
      ["0", false],
      ["false", false],
      ["yes", false],
      ["TRUE", false],
      ["1", true],
      ["true", true],
    ] as const)
      expect(broadcastingOn({ ...E, BROADCAST_ENABLED: v } as Env), String(v)).toBe(on);
  });

  it("the live host: no master, media playlist, segment or heartbeat, with a viewer token that plays while it is on", async () => {
    const { ch, view } = await onAir();
    const media = `${LIVE}/${view}/${ch}/src/index.m3u8`;
    const playlist = await call(media);
    expect(playlist.status).toBe(200);
    const segment = (await playlist.text()).split("\n").find((l) => l && !l.startsWith("#"))!;
    const segURL = new URL(segment, media).toString();
    expect((await call(segURL)).status).toBe(200);
    for (const url of [media, segURL, `${LIVE}/${view}/${ch}/master.m3u8`])
      for (const method of ["GET", "HEAD"]) {
        const r = await call(url, { method }, OFF);
        expect(r.status, `${method} ${url}`).toBe(404);
      }
    await refused(await call(media, {}, OFF), "media playlist");
    await refused(await call(segURL, {}, OFF), "segment");
    await refused(
      await call(`${LIVE}/${view}/${ch}/beat`, { method: "POST", body: "{}" }, OFF),
      "beat"
    );
    // unset is off too
    await refused(await call(media, {}, { BROADCAST_ENABLED: undefined }), "unset");
  });

  it("the watch host: no redeem, code, open session, play, heartbeat or pairing", async () => {
    const ch = newChannel();
    for (const path of [
      "/api/v1/redeem",
      "/api/v1/open/turnstile",
      "/api/v1/open/challenge",
      "/api/v1/open/attest",
      "/api/v1/play",
      "/api/v1/play/s-1/beat",
      "/api/v1/pair",
      "/api/v1/pair/nonce-1",
    ])
      for (const body of [
        { invite: "mkbn1.a.b" },
        { publisher: "home-1", code: "ABCD2345" },
        { publisher: "home-1", channel: ch },
      ])
        await refused(
          await call(
            `${WATCH}${path}`,
            {
              method: "POST",
              headers: { "Content-Type": "application/json", "CF-Connecting-IP": "203.0.113.4" },
              body: JSON.stringify(body),
            },
            OFF
          ),
          path
        );
    await refused(await call(`${WATCH}/api/v1/pair/nonce-1?wait=25`, {}, OFF), "the pair poll");
    await refused(await call(`${WATCH}/i/mkbn1.a.b`, {}, OFF), "an invite link");
    // the kept control path is the configured ingest hostname alone
    await refused(await call(`${WATCH}/v1/control/revocations`, {}, OFF), "control on watch");
    await refused(await call(`${LIVE}/v1/control/revocations`, {}, OFF), "control on live");
    await refused(
      await call("https://ingest.maktaba.tv./v1/control/revocations", {}, OFF),
      "control on trailing-dot ingest host"
    );
    await refused(
      await call("https://other.example/v1/control/revocations", {}, OFF),
      "control on unrecognised host"
    );
    await refused(
      await call(
        `${INGEST}/v1/control/revocations`,
        {},
        { ...OFF, INGEST_HOST: "staging-ingest.maktaba.dev" }
      ),
      "default ingest host when another INGEST_HOST is configured"
    );
    expect(
      (
        await call(
          "https://staging-ingest.maktaba.dev/v1/control/revocations",
          {},
          { ...OFF, INGEST_HOST: "staging-ingest.maktaba.dev" }
        )
      ).status
    ).toBe(200);
    await refused(
      await call(
        "https://staging-ingest.maktaba.dev./v1/control/revocations",
        {},
        { ...OFF, INGEST_HOST: "staging-ingest.maktaba.dev" }
      ),
      "trailing-dot custom ingest host"
    );
  });

  it("the ingest host: a valid token uploads nothing, and nothing is stored", async () => {
    const { ch, token } = await onAir();
    const [segments, guides] = [await stored(E.SEGMENTS), await stored(E.GUIDE)];
    const auth = { Authorization: `Bearer ${token}`, "X-Maktaba-Time": String(now()) };
    // the same calls are taken while it is on (onAir), so it is the switch that refuses
    await refused(await putSegment(ch, "src", 5, { token, env: OFF }), "segment");
    await refused(await putSegment(ch, "slate", 0, { token, env: OFF }), "slate");
    for (const act of ["state", "stream", "guide"])
      await refused(
        await call(
          `${INGEST}/v1/ingest/${ch}/${act}`,
          {
            method: "POST",
            headers: { ...auth, "Content-Type": "application/json" },
            body: JSON.stringify(STATE_3),
          },
          OFF
        ),
        act
      );
    const speed = await ingestToken({ ch, typ: "ingest-speedtest" });
    for (const t of [token, speed])
      await refused(
        await call(
          `${INGEST}/v1/ingest/speedtest`,
          {
            method: "PUT",
            headers: { ...auth, Authorization: `Bearer ${t}` },
            body: segmentBody("src", 1),
          },
          OFF
        ),
        "speedtest"
      );
    expect(await stored(E.SEGMENTS)).toEqual(segments);
    expect(await stored(E.GUIDE)).toEqual(guides);
    // back on, the next segment goes on from the last one: the refused ones left no trace
    advanceClock(1000);
    expect((await putSegment(ch, "src", 5, { token })).status).toBe(201);
  });

  it("the control socket never opens, with a valid token", async () => {
    const ch = newChannel();
    const headers = {
      Upgrade: "websocket",
      Authorization: `Bearer ${await ingestToken({ ch })}`,
      "X-Maktaba-Time": String(now()),
    };
    await refused(await call(`${INGEST}/v1/ingest/ws`, { headers }, OFF), "ws");
    const on = await call(`${INGEST}/v1/ingest/ws`, { headers });
    expect(on.status).toBe(101);
    on.webSocket!.accept();
    on.webSocket!.close();
  });

  it("plans and viewer access are refused: they can give access, the cloud's signature included", async () => {
    await refused(
      await call(
        `${INGEST}/v1/control/plan`,
        { method: "PUT", body: await control("plan", { pub: "home-1", at: now() }) },
        OFF
      ),
      "plan"
    );
    await refused(
      await call(
        `${INGEST}/v1/control/access`,
        { method: "PUT", body: await control("access", { pub: "home-1", version: 1 }) },
        OFF
      ),
      "access"
    );
    await refused(
      await call(
        `${INGEST}/v1/control/passes`,
        { method: "POST", body: await control("passes", { pub: "home-1" }) },
        OFF
      ),
      "passes"
    );
    for (const path of ["/v1/control/", "/v1/control/revocations/", "/v1/control/revocations/x"])
      await refused(await call(`${INGEST}${path}`, { method: "PUT", body: "x" }, OFF), path);
  });

  it("the revocation list still arrives: the cloud's push (signed only) and its version", async () => {
    const ch = newChannel();
    const v = (await revs().current()).version;
    const entry = { id: "r-off", kind: "channel", ref: ch, action: "off_air", reason: "notice" };
    const push = async (body: string) =>
      call(`${INGEST}/v1/control/revocations`, { method: "PUT", body }, OFF);
    expect((await push("not a list")).status).toBe(400);
    expect((await push(await revocationList(v + 1, [entry], "cloud-9"))).status).toBe(401);
    expect((await revs().current()).version).toBe(v);
    expect((await push(await revocationList(v + 1, [entry]))).status).toBe(200);
    expect(await (await call(`${INGEST}/v1/control/revocations`, {}, OFF)).json()).toEqual({
      version: v + 1,
    });
    expect((await revs().current()).entries).toHaveLength(1);
  });

  it("the cron still pulls the list", async () => {
    const V = JSON.parse(E.TEST_VECTORS) as { edge: { seed: string } };
    const v = (await revs().current()).version;
    const body = await revocationList(v + 1, []);
    const asked: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      asked.push(String(input instanceof Request ? input.url : input));
      return new Response(body);
    });
    const ctx = createExecutionContext();
    await worker.scheduled(
      createScheduledController(),
      {
        ...E,
        ...OFF,
        API_ORIGIN: "https://api.example.test",
        EDGE_SIGNING_KEY: V.edge.seed,
      } as Env,
      ctx
    );
    await waitOnExecutionContext(ctx);
    expect(asked).toEqual(["https://api.example.test/v1/broadcast/revocations"]);
    expect((await revs().current()).version).toBe(v + 1);
  });
});
