// The edge↔cloud contract (cloud/internal/broadcast/edge.go): the edge builds its usage post,
// reads the cloud's reply and its plan push exactly as the shared vectors say, the same bytes
// the Go tests check against the cloud's handler (cloud/internal/handlers/edge).
import { runInDurableObject } from "cloudflare:test";
import { afterEach, describe, expect, it, vi } from "vitest";
import { utf8 } from "../src/crypto";
import { signEdgeRequest } from "../src/edge_sign";
import { resetClock } from "../src/clock";
import {
  newUsageWindow,
  parsePlan,
  PublisherControl,
  readUsageReply,
  rfc3339,
  USAGE_MAX_WINDOWS,
  USAGE_PATH,
  WINDOW_MS,
  type PlanState,
  type UsageWindow,
} from "../src/publisher";
import { verifyControl } from "../src/tokens";
import { E } from "./helpers";

const V = JSON.parse(E.TEST_VECTORS) as {
  at_ms: number;
  cloud_pubkeys: string;
  plan: string;
  edge: {
    seed: string;
    method: string;
    path: string;
    ms: number;
    body: string;
    signature: string;
    usage_reply: string;
  };
  state: { publisher: string };
  plan_state: {
    pub: string;
    version: number;
    mode: PlanState["mode"];
    max_rungs?: number;
    max_top_kbps?: number;
    max_viewers?: number;
  };
};

/** The PlanState the edge holds for the vectors' plan. */
const wantPlan: PlanState = {
  mode: V.plan_state.mode,
  version: V.plan_state.version,
  max_rungs: V.plan_state.max_rungs,
  max_top_kbps: V.plan_state.max_top_kbps,
  max_viewers: V.plan_state.max_viewers,
  delivery: null,
};

afterEach(() => {
  resetClock();
  vi.restoreAllMocks();
});

describe("the usage post, byte for byte as the cloud reads it", () => {
  it("builds the vectors' body and signs it as Go does", async () => {
    const w = newUsageWindow(V.state.publisher, V.at_ms - WINDOW_MS);
    Object.assign(w, {
      minutes_full: 12,
      minutes_lowest: 3,
      minutes_stream: 2,
      segments_in: 150,
      bytes_in: 123456789,
      peak_sessions: 9,
    });
    const body = JSON.stringify({ edge: "broadcast-edge", windows: [w] });
    expect(body).toBe(V.edge.body);
    const h = await signEdgeRequest(V.edge.seed, "POST", USAGE_PATH, V.edge.ms, utf8(body));
    expect(V.edge.path).toBe(USAGE_PATH);
    expect(h).toEqual({
      "X-Maktaba-Edge-Time": String(V.edge.ms),
      "X-Maktaba-Edge-Signature": V.edge.signature,
    });
  });

  it("keys a window <publisher>:<window_start> in RFC 3339 whole seconds", () => {
    const w = newUsageWindow("p", Date.UTC(2026, 10, 13, 0, 5));
    expect(w.key).toBe("p:2026-11-13T00:05:00Z");
    expect(w.window_end).toBe("2026-11-13T00:10:00Z");
    expect(rfc3339(Date.UTC(2026, 10, 13, 0, 5, 7, 999))).toBe("2026-11-13T00:05:07Z");
  });
});

describe("the cloud's answers", () => {
  it("reads the vectors' usage reply: the taken key is done, the publisher's plan applies", () => {
    const r = readUsageReply(JSON.parse(V.edge.usage_reply));
    expect(r.done).toEqual([JSON.parse(V.edge.body).windows[0].key]);
    expect(r.plans).toEqual([{ pub: V.state.publisher, plan: wantPlan }]);
  });

  it("reads the vectors' plan push as the same plan", async () => {
    const v = await verifyControl(V.plan, "plan", V.cloud_pubkeys, V.at_ms);
    expect(v.ok).toBe(true);
    if (v.ok) expect(parsePlan(v.payload)).toEqual({ pub: V.state.publisher, plan: wantPlan });
  });

  it("refuses a plan without a publisher, a version or a known mode", () => {
    expect(parsePlan({ version: 1, mode: "full" })).toBeNull();
    expect(parsePlan({ pub: "p", mode: "full" })).toBeNull();
    expect(parsePlan({ pub: "p", version: 1, mode: "capped" })).toBeNull();
    expect(parsePlan(null)).toBeNull();
  });

  it("treats duplicates and rejected windows as done, and ignores junk", () => {
    expect(
      readUsageReply({
        taken: ["a"],
        duplicates: ["b"],
        rejected: [{ key: "c", error: "bad_window" }, null, { error: "x" }],
        plans: [{ pub: "p" }],
      })
    ).toEqual({ done: ["a", "b", "c"], plans: [] });
    expect(readUsageReply("nope")).toEqual({ done: [], plans: [] });
  });
});

describe("PublisherControl posts its queue", () => {
  const pub = V.state.publisher;

  /** n closed windows, oldest first, ending at least one window before now. */
  function closed(n: number): Record<string, UsageWindow & { sent: boolean }> {
    const last = Math.floor(Date.now() / WINDOW_MS) * WINDOW_MS - WINDOW_MS;
    const out: Record<string, UsageWindow & { sent: boolean }> = {};
    for (let i = 0; i < n; i++) {
      const start = last - (n - 1 - i) * WINDOW_MS;
      out[String(start)] = { ...newUsageWindow(pub, start), minutes_full: 1, sent: false };
    }
    return out;
  }

  it("in pages of 2,000, signed, keeping every window the reply doesn't name", async () => {
    const stub = E.PUBLISHER.get(E.PUBLISHER.idFromName("contract-paging"));
    const posts: { headers: Headers; windows: UsageWindow[] }[] = [];
    const result = await runInDurableObject(stub, async (o: PublisherControl) => {
      const self = o as unknown as {
        env: Record<string, unknown>;
        s: { pub: string; windows: Record<string, UsageWindow & { sent: boolean }> };
      };
      self.env = { ...self.env, API_ORIGIN: "https://api.test", EDGE_SIGNING_KEY: V.edge.seed };
      self.s.pub = pub;
      self.s.windows = closed(USAGE_MAX_WINDOWS + 3);
      vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
        expect(String(input)).toBe("https://api.test" + USAGE_PATH);
        const windows = (
          JSON.parse(String(new TextDecoder().decode(init?.body as Uint8Array))) as {
            windows: UsageWindow[];
          }
        ).windows;
        posts.push({ headers: new Headers(init?.headers), windows });
        // the cloud takes all but the newest window of the last page (it stays for later)
        const keys = windows.map((w) => w.key);
        const last = posts.length === 2;
        return Response.json({
          taken: last ? keys.slice(0, -1) : keys,
          duplicates: [],
          rejected: [],
          plans: [{ pub, version: 7, mode: "lowest", max_rungs: 1 }],
        });
      });
      await o.alarm();
      return { pending: await o.pendingUsage(), plan: await o.plan() };
    });
    expect(posts.map((p) => p.windows.length)).toEqual([USAGE_MAX_WINDOWS, 3]);
    expect(posts[0]!.headers.get("X-Maktaba-Edge-Time")).toMatch(/^\d{13}$/);
    expect(posts[0]!.headers.get("X-Maktaba-Edge-Signature")).toMatch(/^[A-Za-z0-9_-]{86}$/);
    expect(posts[0]!.windows[0]!.key).toMatch(/^.+:\d{4}-\d\d-\d\dT\d\d:\d\d:00Z$/);
    expect(result.pending.map((w) => w.key)).toEqual([posts[1]!.windows[2]!.key]);
    expect(result.plan).toMatchObject({ mode: "lowest", version: 7, max_rungs: 1 });
  });

  it("keeps the whole queue when the cloud fails", async () => {
    const stub = E.PUBLISHER.get(E.PUBLISHER.idFromName("contract-outage"));
    const pending = await runInDurableObject(stub, async (o: PublisherControl) => {
      const self = o as unknown as {
        env: Record<string, unknown>;
        s: { pub: string; windows: Record<string, UsageWindow & { sent: boolean }> };
      };
      self.env = { ...self.env, API_ORIGIN: "https://api.test", EDGE_SIGNING_KEY: V.edge.seed };
      self.s.pub = pub;
      self.s.windows = closed(4);
      vi.spyOn(globalThis, "fetch").mockResolvedValue(
        Response.json({ error: "try_again" }, { status: 503 })
      );
      await o.alarm();
      return o.pendingUsage();
    });
    expect(pending).toHaveLength(4);
  });
});
