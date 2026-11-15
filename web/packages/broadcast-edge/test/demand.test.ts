// On demand (story 50.22 §2, 50.26): the channel wakes an on-demand home at its first viewer,
// puts it to sleep once the last has gone (no heartbeat for SESSION_TTL_MS), never sends an
// always-on home either, and sends `limits` when the plan's rungs or mode leave rungs no viewer
// can play.
import { runDurableObjectAlarm } from "cloudflare:test";
import { afterEach, describe, expect, it, vi } from "vitest";
import { advanceClock, now, resetClock } from "../src/clock";
import { SESSION_TTL_MS } from "../src/limits";
import {
  call,
  control,
  E,
  ingestToken,
  INGEST,
  LIVE,
  newChannel,
  putSegment,
  viewerToken,
} from "./helpers";

afterEach(() => resetClock());

function stub(ch: string) {
  return E.CHANNEL.get(E.CHANNEL.idFromName(ch));
}

interface Home {
  got: Record<string, unknown>[];
  types: () => string[];
  close: () => void;
}

/** connect opens a home's control socket for ch, naming its mode (absent: none named). */
async function connect(ch: string, pub: string, mode?: string): Promise<Home> {
  const headers: Record<string, string> = {
    Upgrade: "websocket",
    Authorization: `Bearer ${await ingestToken({ ch, pub })}`,
    "X-Maktaba-Time": String(now()),
  };
  if (mode) headers["X-Maktaba-Mode"] = mode;
  const res = await call(`${INGEST}/v1/ingest/ws`, { headers });
  expect(res.status).toBe(101);
  const ws = res.webSocket!;
  const got: Record<string, unknown>[] = [];
  ws.addEventListener("message", (e) => got.push(JSON.parse(String(e.data))));
  ws.accept();
  await vi.waitFor(() => expect(got.map((m) => m.type)).toContain("hello"));
  return { got, types: () => got.map((m) => String(m.type)), close: () => ws.close() };
}

async function master(ch: string, pub: string, session: string, mode: "full" | "lowest" = "full") {
  return call(`${LIVE}/${await viewerToken({ ch, pub, session, mode })}/${ch}/master.m3u8`);
}

async function beat(ch: string, pub: string, session: string) {
  const r = await call(`${LIVE}/${await viewerToken({ ch, pub, session })}/${ch}/beat`, {
    method: "POST",
    body: "{}",
  });
  expect(r.status).toBe(200);
}

/** settle gives a message that is NOT expected the time it would take to arrive. */
const settle = () => new Promise((r) => setTimeout(r, 100));

describe("on-demand wake and sleep", () => {
  it("wakes the home at the first viewer, shows waiting until the first segment, and sleeps after the last viewer's heartbeat ends", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const home = await connect(ch, pub, "on_demand");
    // asleep, nobody watching: waiting (it starts when someone comes), and no wake
    expect(home.got[0]).toMatchObject({ type: "hello", status: "waiting" });
    await settle();
    expect(home.types()).not.toContain("wake");

    await master(ch, pub, "s-first-viewer-1");
    await vi.waitFor(() => expect(home.types()).toContain("wake"));
    expect((await stub(ch).snapshot()).status).toBe("waiting");
    // a second viewer wakes nothing more
    await master(ch, pub, "s-second-viewr-2");
    await settle();
    expect(home.types().filter((x) => x === "wake")).toHaveLength(1);

    // the home starts: the first segment makes it live
    expect((await putSegment(ch, "src", 1, { token: await ingestToken({ ch, pub }) })).status).toBe(
      201
    );
    expect((await stub(ch).snapshot()).status).toBe("live");

    // heartbeats keep it awake past the session life
    advanceClock(SESSION_TTL_MS - 30_000);
    await beat(ch, pub, "s-first-viewer-1");
    advanceClock(60_000);
    expect(await runDurableObjectAlarm(stub(ch))).toBe(true);
    await settle();
    expect(home.types()).not.toContain("sleep");

    // no heartbeat for the session life: the last viewer has gone
    advanceClock(SESSION_TTL_MS + 1000);
    expect(await runDurableObjectAlarm(stub(ch))).toBe(true);
    await vi.waitFor(() => expect(home.types()).toContain("sleep"));
    expect(home.types().filter((x) => x === "sleep")).toHaveLength(1);

    // a viewer again: a new wake
    await master(ch, pub, "s-come-back-333");
    await vi.waitFor(() => expect(home.types().filter((x) => x === "wake")).toHaveLength(2));
    home.close();
  });

  it("tells a reconnecting home it is woken while viewers watch", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const first = await connect(ch, pub, "on_demand");
    await master(ch, pub, "s-viewer-recon-1");
    await vi.waitFor(() => expect(first.types()).toContain("wake"));
    first.close();
    // the home restarted (its woken map is gone): the new socket hears the wake at once
    const again = await connect(ch, pub, "on_demand");
    await vi.waitFor(() => expect(again.types()).toContain("wake"));
    again.close();
  });

  it("a viewer who arrived before the home connected wakes it on connect", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    await connect(ch, pub, "on_demand").then((h) => h.close());
    await master(ch, pub, "s-early-viewer-1");
    const home = await connect(ch, pub, "on_demand");
    await vi.waitFor(() => expect(home.types()).toContain("wake"));
    home.close();
  });

  it("never sends an always-on home wake or sleep", async () => {
    for (const mode of ["always_on", undefined]) {
      const ch = newChannel();
      const pub = `pub-${ch}`;
      const home = await connect(ch, pub, mode);
      await putSegment(ch, "src", 1, { token: await ingestToken({ ch, pub }) });
      await master(ch, pub, "s-always-on-v-1");
      advanceClock(SESSION_TTL_MS + 61_000);
      expect(await runDurableObjectAlarm(stub(ch))).toBe(true);
      await settle();
      expect(home.types()).not.toContain("wake");
      expect(home.types()).not.toContain("sleep");
      home.close();
    }
  });
});

describe("limits", () => {
  async function pushPlan(pub: string, over: Record<string, unknown>) {
    const r = await call(`${INGEST}/v1/control/plan`, {
      method: "PUT",
      body: await control("plan", { pub, mode: "full", version: now(), ...over }),
    });
    expect(r.status, await r.clone().text()).toBe(200);
  }

  it("sends the plan's rungs, then only the lowest in `lowest` once no full viewer is left, then all again", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    // the channel registers with its publisher at its first segment
    await putSegment(ch, "src", 1, { token: await ingestToken({ ch, pub }) });
    const home = await connect(ch, pub, "always_on");
    const limits = () => home.got.filter((m) => m.type === "limits").map((m) => m.rungs);

    await pushPlan(pub, { max_rungs: 2 });
    await vi.waitFor(() => expect(limits()).toEqual([["src", "720p"]]));

    // a full viewer playing: `lowest` (new sessions) leaves the home's rungs alone
    await master(ch, pub, "s-full-viewer-01", "full");
    await pushPlan(pub, { mode: "lowest", max_rungs: 2 });
    await settle();
    expect(limits()).toHaveLength(1);
    // the full viewer gone: only the lowest of the plan's rungs
    advanceClock(SESSION_TTL_MS + 1000);
    expect(await runDurableObjectAlarm(stub(ch))).toBe(true);
    await vi.waitFor(() => expect(limits().at(-1)).toEqual(["720p"]));

    // a reconnecting home hears the current limits
    const again = await connect(ch, pub, "always_on");
    await vi.waitFor(() =>
      expect(again.got.find((m) => m.type === "limits")).toMatchObject({ rungs: ["720p"] })
    );
    again.close();

    // the plan back to full with every rung: no limit
    await pushPlan(pub, { mode: "full" });
    await vi.waitFor(() => expect(limits().at(-1)).toEqual([]));
    home.close();
  });
});
