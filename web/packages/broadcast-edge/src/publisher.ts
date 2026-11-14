// PublisherControl (story 50.21 §1): one Durable Object per publisher (the home: the ingest
// token's pub is its server id, the id the cloud bills). It holds what the cloud says the
// publisher's plan allows (a signed push to PUT /v1/control/plan, and the same Plan in every
// usage reply, 50.20 §2), the channels the publisher has published (so a revocation reaches
// them, 50.25), the viewer sessions' heartbeats (the concurrent count, 50.26) and the metered
// viewer-minutes, kept in 5-minute windows for 7 days and posted to the API (50.20 §3).
//
// The wire shapes here are the cloud's (cloud/internal/broadcast/edge.go: UsageWindow,
// UsagePost, UsageReply, Plan), locked by the vectors both sides test
// (cloud/internal/broadcast/testdata/vectors.json).
//
// The seams later stories fill:
//   - setPlan(plan)          50.20: the cloud's plan state; `mode` is what a NEW session gets
//   - beat(...)              50.26: a session's 60 s heartbeat → one viewer-minute (full/lowest)
//   - pendingUsage()/ack()   50.20: the windows not yet accepted by the API, with their keys
//   - channels()             50.25: fan-out of a publisher-wide revocation
import { DurableObject } from "cloudflare:workers";
import { now } from "./clock";
import { signEdgeRequest } from "./edge_sign";
import type { Delivery, Env } from "./env";
import { log } from "./log";
import type { Mode } from "./tokens";

/** A heartbeat counts one viewer-minute; beats closer than this to the last counted are ignored. */
export const BEAT_MIN_GAP_MS = 50_000;
/** A session is concurrent while its last beat is younger than this (50.20 §2). */
export const SESSION_LIVE_MS = 150_000;
export const WINDOW_MS = 5 * 60_000;
export const KEEP_WINDOWS_MS = 7 * 24 * 3600_000;
export const USAGE_PATH = "/_edge/usage";
/** The most windows one usage post carries (the cloud refuses more); a longer queue goes in pages. */
export const USAGE_MAX_WINDOWS = 2000;

/** The plan state the cloud pushes (a cloud-signed control post, typ "plan") and returns in
 * every usage reply (its `plans`): broadcast.Plan in Go. */
export interface PlanState {
  /** the mode a new viewer session gets (50.20 §2) */
  mode: Mode;
  max_rungs?: number;
  max_top_kbps?: number;
  max_viewers?: number;
  /** a per-publisher delivery override (§5); null or absent follows BROADCAST_DELIVERY */
  delivery?: Delivery | null;
  /** the cloud's version of this state: an older one never replaces a newer */
  version: number;
}

/**
 * One 5-minute window of a publisher's usage: the report shape the edge posts to the API
 * (50.20 §3; broadcast.UsageWindow in Go, the same fields in the same order). `key` is the
 * idempotency key `<publisher>:<window_start>`, RFC 3339 UTC in whole seconds. The cloud bills
 * minutes_full and minutes_lowest; the other counters it checks and does not store yet.
 */
export interface UsageWindow {
  key: string;
  publisher: string;
  window_start: string;
  window_end: string;
  minutes_full: number;
  minutes_lowest: number;
  /** the viewer-minutes of those that went through the Stream fallback ($0.06 a viewer-hour) */
  minutes_stream: number;
  segments_in: number;
  bytes_in: number;
  peak_sessions: number;
}

interface State {
  pub: string;
  plan: PlanState | null;
  channels: string[];
  sessions: Record<string, { at: number; ch: string }>;
  windows: Record<string, UsageWindow & { sent: boolean }>;
}

export type BeatResult = { counted: boolean; concurrent: number; delivery: Delivery };

export class PublisherControl extends DurableObject<Env> {
  private s: State = { pub: "", plan: null, channels: [], sessions: {}, windows: {} };

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    ctx.blockConcurrencyWhile(async () => {
      const saved = await ctx.storage.get<State>("s");
      if (saved) this.s = saved;
    });
  }

  private async save(): Promise<void> {
    await this.ctx.storage.put("s", this.s);
    if ((await this.ctx.storage.getAlarm()) === null)
      await this.ctx.storage.setAlarm(Date.now() + WINDOW_MS);
  }

  private window(t: number): UsageWindow & { sent: boolean } {
    const start = Math.floor(t / WINDOW_MS) * WINDOW_MS;
    const id = String(start);
    let w = this.s.windows[id];
    if (!w) {
      w = { ...newUsageWindow(this.s.pub, start), sent: false };
      this.s.windows[id] = w;
    }
    return w;
  }

  /** setPlan takes the cloud's plan state and passes what channels enforce on to them. */
  async setPlan(pub: string, plan: PlanState): Promise<"applied" | "stale"> {
    this.s.pub ||= pub;
    if (this.s.plan && plan.version < this.s.plan.version) return "stale";
    this.s.plan = plan;
    await this.save();
    await Promise.all(
      this.s.channels.map((ch) =>
        this.env.CHANNEL.get(this.env.CHANNEL.idFromName(ch)).setPlan({
          off: plan.mode === "off",
          max_rungs: plan.max_rungs,
          max_top_kbps: plan.max_top_kbps,
          delivery: plan.delivery ?? null,
        })
      )
    );
    return "applied";
  }

  async plan(): Promise<PlanState | null> {
    return this.s.plan;
  }

  async addChannel(ch: string): Promise<void> {
    if (this.s.channels.includes(ch)) return;
    this.s.channels.push(ch);
    await this.save();
    const p = this.s.plan;
    if (p) {
      await this.env.CHANNEL.get(this.env.CHANNEL.idFromName(ch)).setPlan({
        off: p.mode === "off",
        max_rungs: p.max_rungs,
        max_top_kbps: p.max_top_kbps,
        delivery: p.delivery ?? null,
      });
    }
  }

  async channels(): Promise<string[]> {
    return this.s.channels;
  }

  delivery(): Delivery {
    const d = this.s.plan?.delivery;
    if (d === "stream" || d === "r2") return d;
    return this.env.BROADCAST_DELIVERY === "stream" ? "stream" : "r2";
  }

  /** beat records a session's heartbeat: one viewer-minute per 60 s in `full` or `lowest`. */
  async beat(pub: string, session: string, ch: string, mode: Mode): Promise<BeatResult> {
    this.s.pub ||= pub;
    const t = now();
    for (const [k, v] of Object.entries(this.s.sessions))
      if (t - v.at > SESSION_LIVE_MS) delete this.s.sessions[k];
    const last = this.s.sessions[session];
    const delivery = this.delivery();
    let counted = false;
    if ((mode === "full" || mode === "lowest") && (!last || t - last.at >= BEAT_MIN_GAP_MS)) {
      this.s.sessions[session] = { at: t, ch };
      const w = this.window(t);
      if (mode === "full") w.minutes_full++;
      else w.minutes_lowest++;
      if (delivery === "stream") w.minutes_stream++;
      w.peak_sessions = Math.max(w.peak_sessions, Object.keys(this.s.sessions).length);
      counted = true;
      this.env.AE?.writeDataPoint({
        blobs: ["viewer_minute", ch, mode, delivery],
        doubles: [1],
        indexes: [ch],
      });
      await this.save();
    }
    return { counted, concurrent: Object.keys(this.s.sessions).length, delivery };
  }

  async recordIngest(segments: number, bytes: number): Promise<void> {
    const w = this.window(now());
    w.segments_in += segments;
    w.bytes_in += bytes;
    await this.save();
  }

  /** pendingUsage is every window the API has not accepted yet, oldest first. */
  async pendingUsage(): Promise<UsageWindow[]> {
    return Object.keys(this.s.windows)
      .sort()
      .map((k) => this.s.windows[k]!)
      .filter((w) => !w.sent)
      .map(({ sent: _sent, ...w }) => w);
  }

  async ack(keys: string[]): Promise<void> {
    const done = new Set(keys);
    for (const w of Object.values(this.s.windows)) if (done.has(w.key)) w.sent = true;
    await this.save();
  }

  /** Every 5 minutes: post the closed windows to the API (when one is set), drop old ones. */
  override async alarm(): Promise<void> {
    const t = now();
    for (const [k, w] of Object.entries(this.s.windows)) {
      if (t - Date.parse(w.window_end) > KEEP_WINDOWS_MS) delete this.s.windows[k];
    }
    const closed = (await this.pendingUsage()).filter((w) => Date.parse(w.window_end) <= t);
    if (closed.length > 0 && this.env.API_ORIGIN && this.env.EDGE_SIGNING_KEY) {
      try {
        for (let i = 0; i < closed.length; i += USAGE_MAX_WINDOWS) {
          const reply = readUsageReply(await this.post(closed.slice(i, i + USAGE_MAX_WINDOWS), t));
          // taken, duplicate and rejected windows are done; any other stays for the next post
          await this.ack(reply.done);
          const plan = reply.plans.find((p) => p.pub === this.s.pub);
          if (plan) await this.setPlan(plan.pub, plan.plan);
        }
      } catch (err) {
        // kept, and posted again later with the same keys
        log("warn", "usage not posted", { component: "publisher", pub: this.s.pub, err });
      }
    }
    await this.ctx.storage.put("s", this.s);
    await this.ctx.storage.setAlarm(Date.now() + WINDOW_MS);
  }

  private async post(windows: UsageWindow[], t: number): Promise<unknown> {
    const body = usageBody(windows);
    const headers = await signEdgeRequest(
      this.env.EDGE_SIGNING_KEY ?? "",
      "POST",
      USAGE_PATH,
      t,
      body
    );
    const res = await fetch(new URL(USAGE_PATH, this.env.API_ORIGIN).toString(), {
      method: "POST",
      headers: { ...headers, "Content-Type": "application/json" },
      body,
    });
    if (!res.ok) throw new Error(`usage post answered ${res.status}`);
    return res.json();
  }
}

/** rfc3339 is a time as Go writes it in JSON: UTC, whole seconds, no ".000". */
export function rfc3339(ms: number): string {
  return new Date(ms).toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** newUsageWindow is the empty window of a publisher that starts at startMs. */
export function newUsageWindow(pub: string, startMs: number): UsageWindow {
  const start = rfc3339(startMs);
  return {
    key: `${pub}:${start}`,
    publisher: pub,
    window_start: start,
    window_end: rfc3339(startMs + WINDOW_MS),
    minutes_full: 0,
    minutes_lowest: 0,
    minutes_stream: 0,
    segments_in: 0,
    bytes_in: 0,
    peak_sessions: 0,
  };
}

/** usageBody is a usage post's bytes (broadcast.UsagePost). */
export function usageBody(windows: UsageWindow[]): Uint8Array {
  return new TextEncoder().encode(JSON.stringify({ edge: "broadcast-edge", windows }));
}

const MODES = ["full", "lowest", "wait", "off"];

/**
 * parsePlan reads a Plan (broadcast.Plan) from a plan push's payload or a usage reply's `plans`;
 * null when it isn't one.
 */
export function parsePlan(p: unknown): { pub: string; plan: PlanState } | null {
  if (typeof p !== "object" || p === null) return null;
  const o = p as Record<string, unknown>;
  if (
    typeof o.pub !== "string" ||
    o.pub === "" ||
    typeof o.version !== "number" ||
    typeof o.mode !== "string" ||
    !MODES.includes(o.mode)
  )
    return null;
  const num = (x: unknown) => (typeof x === "number" && x > 0 ? x : undefined);
  return {
    pub: o.pub,
    plan: {
      mode: o.mode as Mode,
      version: o.version,
      max_rungs: num(o.max_rungs),
      max_top_kbps: num(o.max_top_kbps),
      max_viewers: num(o.max_viewers),
      delivery: o.delivery === "stream" || o.delivery === "r2" ? o.delivery : null,
    },
  };
}

/**
 * readUsageReply reads the cloud's answer to a usage post (broadcast.UsageReply): the keys that
 * are done (taken, duplicate or rejected: never posted again) and each publisher's plan.
 */
export function readUsageReply(r: unknown): {
  done: string[];
  plans: { pub: string; plan: PlanState }[];
} {
  const o = (typeof r === "object" && r !== null ? r : {}) as Record<string, unknown>;
  const keys = (x: unknown) =>
    Array.isArray(x) ? x.filter((k): k is string => typeof k === "string") : [];
  const rejected = Array.isArray(o.rejected)
    ? o.rejected.map((x) => (x as { key?: unknown } | null)?.key)
    : [];
  const plans = Array.isArray(o.plans) ? o.plans.map(parsePlan) : [];
  return {
    done: [...keys(o.taken), ...keys(o.duplicates), ...keys(rejected)],
    plans: plans.filter((p): p is { pub: string; plan: PlanState } => p !== null),
  };
}
