// A heartbeat's storage writes (story 50.21 §3, 50.20 §2, 50.29's 1,000-viewer fan-out): each
// viewer of a channel and each session of a publisher is its own key, so a beat writes what it
// changed and never the whole state. Rewriting every viewer, every segment key and seven days of
// usage windows on each beat was O(viewers) a beat, O(viewers²) a minute; at 1,000 viewers the
// channel's ingest PUTs waited seconds behind it (nightly 43683183048).
import { runInDurableObject } from "cloudflare:test";
import { afterEach, describe, expect, it } from "vitest";
import { advanceClock, resetClock } from "../src/clock";
import type { ChannelLive } from "../src/channel_live";
import { BEAT_MIN_GAP_MS, type PublisherControl } from "../src/publisher";
import { E, newChannel } from "./helpers";

afterEach(() => resetClock());

/** writes records every key the object's storage puts or deletes while fn runs. */
async function writes(
  ctx: DurableObjectState,
  fn: () => Promise<unknown>
): Promise<{ put: string[]; deleted: string[] }> {
  const st = ctx.storage as unknown as {
    put: (...a: unknown[]) => Promise<void>;
    delete: (...a: unknown[]) => Promise<unknown>;
  };
  const put: string[] = [];
  const deleted: string[] = [];
  const realPut = st.put.bind(st);
  const realDelete = st.delete.bind(st);
  st.put = (k: unknown, v?: unknown) => {
    if (typeof k === "string") put.push(k);
    else put.push(...Object.keys(k as Record<string, unknown>));
    return v === undefined ? realPut(k) : realPut(k, v);
  };
  st.delete = (k: unknown) => {
    deleted.push(...(Array.isArray(k) ? (k as string[]) : [k as string]));
    return realDelete(k);
  };
  try {
    await fn();
  } finally {
    st.put = realPut;
    st.delete = realDelete;
  }
  return { put, deleted };
}

describe("ChannelLive: a heartbeat writes its viewer only", () => {
  it("writes a new viewer's key (and the state only when the home's demand changed), not on a repeat beat; the alarm writes the refreshed ones", async () => {
    const ch = newChannel();
    const stub = E.CHANNEL.get(E.CHANNEL.idFromName(ch));
    await runInDurableObject(stub, async (o: ChannelLive, state) => {
      // 1,000 viewers already watching
      for (let i = 0; i < 1000; i++) await o.viewer(`s-${i}`, "full");
      const first = await writes(state, () => o.viewer("s-new", "full"));
      expect(first.put).toEqual(["v:s-new"]); // the home was already told: no "s"
      // a changed mode may change the limits: written at once
      const lowest = await writes(state, () => o.viewer("s-1", "lowest"));
      expect(lowest.put).toContain("v:s-1");
      advanceClock(30_000);
      const again = await writes(state, () => o.viewer("s-new", "full"));
      expect(again.put).toEqual([]);
      const alarm = await writes(state, () => o.alarm());
      expect(alarm.put).toContain("s");
      expect(alarm.put).toContain("v:s-new");
      expect(alarm.put).not.toContain("v:s-2"); // not beaten since its last write
      const saved = await state.storage.get<Record<string, unknown>>("s");
      expect(saved?.viewers).toBeUndefined();
      expect(await state.storage.get("v:s-new")).toMatchObject({ mode: "full" });
    });
  });

  it("deletes a lapsed viewer's key", async () => {
    const ch = newChannel();
    const stub = E.CHANNEL.get(E.CHANNEL.idFromName(ch));
    await runInDurableObject(stub, async (o: ChannelLive, state) => {
      await o.viewer("s-old", "full");
      advanceClock(200_000); // past SESSION_TTL_MS
      const w = await writes(state, () => o.viewer("s-young", "full"));
      expect(w.deleted).toEqual(["v:s-old"]);
      expect(await state.storage.get("v:s-old")).toBeUndefined();
    });
  });

  it("reads viewers kept in the earlier layout (inside the state) and moves each to its key", async () => {
    const ch = newChannel();
    const stub = E.CHANNEL.get(E.CHANNEL.idFromName(ch));
    await runInDurableObject(stub, async (o: ChannelLive, state) => {
      await state.storage.put("s", { viewers: { "s-a": { at: Date.now(), mode: "full" } } });
      await state.storage.put("v:s-b", { at: Date.now(), mode: "lowest" });
      const Ctor = o.constructor as new (s: DurableObjectState, e: unknown) => ChannelLive;
      const fresh = new Ctor(state, E);
      await state.blockConcurrencyWhile(async () => {}); // the constructor's load has run
      const viewers = (fresh as unknown as { s: { viewers: Record<string, unknown> } }).s.viewers;
      expect(Object.keys(viewers).sort()).toEqual(["s-a", "s-b"]);
      expect(await state.storage.get("v:s-a")).toMatchObject({ mode: "full" });
      expect((await state.storage.get<Record<string, unknown>>("s"))?.viewers).toBeUndefined();
    });
  });
});

describe("PublisherControl: a counted beat writes its session and its window", () => {
  it("never rewrites every session or every window", async () => {
    const pub = `pub-${newChannel()}`;
    const stub = E.PUBLISHER.get(E.PUBLISHER.idFromName(pub));
    await runInDurableObject(stub, async (o: PublisherControl, state) => {
      for (let i = 0; i < 1000; i++) await o.beat(pub, `s-${i}`, "ch", "full");
      const w = await writes(state, () => o.beat(pub, "s-new", "ch", "full"));
      expect(w.put).toHaveLength(2);
      expect(w.put).toContain("x:s-new");
      expect(w.put.filter((k) => k.startsWith("w:"))).toHaveLength(1);
      // a beat inside the gap counts nothing and writes nothing
      const early = await writes(state, () => o.beat(pub, "s-new", "ch", "full"));
      expect(early.put).toEqual([]);
      // the counted minute survives a restart: the windows and sessions are read back
      advanceClock(BEAT_MIN_GAP_MS);
      await o.beat(pub, "s-new", "ch", "full");
      const Ctor = o.constructor as new (s: DurableObjectState, e: unknown) => PublisherControl;
      const fresh = new Ctor(state, E);
      await state.blockConcurrencyWhile(async () => {});
      const before = await o.pendingUsage();
      expect(await fresh.pendingUsage()).toEqual(before);
      const minutes = before.reduce((n, x) => n + x.minutes_full, 0);
      expect(minutes).toBe(1002);
      const saved = await state.storage.get<Record<string, unknown>>("s");
      expect(saved?.sessions).toBeUndefined();
      expect(saved?.windows).toBeUndefined();
    });
  });

  it("reads sessions and windows kept in the earlier layout and moves each to its key", async () => {
    const pub = `pub-${newChannel()}`;
    const stub = E.PUBLISHER.get(E.PUBLISHER.idFromName(pub));
    await runInDurableObject(stub, async (o: PublisherControl, state) => {
      await o.beat(pub, "s-a", "ch", "full");
      const windows = await o.pendingUsage();
      const keys = [...(await state.storage.list()).keys()];
      await state.storage.delete(keys);
      await state.storage.put("s", {
        pub,
        plan: null,
        channels: ["ch"],
        sessions: { "s-a": { at: Date.now(), ch: "ch" } },
        windows: Object.fromEntries(
          windows.map((w) => [String(Date.parse(w.window_start)), { ...w, sent: false }])
        ),
      });
      const Ctor = o.constructor as new (s: DurableObjectState, e: unknown) => PublisherControl;
      const fresh = new Ctor(state, E);
      await state.blockConcurrencyWhile(async () => {});
      expect(await fresh.pendingUsage()).toEqual(windows);
      expect(await fresh.channels()).toEqual(["ch"]);
      expect(await state.storage.get("x:s-a")).toMatchObject({ ch: "ch" });
      const saved = await state.storage.get<Record<string, unknown>>("s");
      expect(saved?.sessions).toBeUndefined();
      expect(saved).toMatchObject({ pub, channels: ["ch"] });
    });
  });
});
