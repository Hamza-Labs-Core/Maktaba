// ChannelLive (story 50.21 §3): one Durable Object per published channel. It keeps each rung's
// last 30 segments and the channel's live state, writes the playlists, splices in the offline
// slate, enforces the per-rung burst guard and the publisher's plan, and deletes segment objects
// 10 minutes after they were written (an alarm every 60 s). The home's control WebSocket lands
// here too, so the channel can tell the home a rung stalled or the channel was revoked.
//
// Revocations (50.25): the channel holds the list's verdict for itself (its channel, its
// publisher, its home, or a global entry), handed to it on each install and re-read from the
// Revocations object at most every 10 s. An off_air verdict refuses ingest (403 revoked) and
// play (503 off_air with the reason, so a playing viewer gets the off-air card at the next
// playlist); a slate verdict loops the slate in every rung's playlist until it ends. The home
// hears each change on its control socket: revoked, slate, restored.
//
// On demand (50.22 §2): the home names its channel's mode when its control socket connects. The
// channel keeps its viewers (each play, master playlist and heartbeat, by session), sends an
// on-demand home `wake` at the first one, and `sleep` once the last has gone (no heartbeat for
// SESSION_TTL_MS; the home then stops after its own 5-minute grace). An always-on channel never
// gets either. It sends `limits` (the rungs the home should send, top first; [] for all) when the
// plan's rung count or mode leaves a rung no viewer can play: in `lowest` with no `full` viewer
// left, only the lowest rung.
//
// It never looks inside a segment and never decides what airs: it orders what the home sent.
import type { Geo } from "./geo";
import { SESSION_TTL_MS } from "./limits";
import type { Mode } from "./tokens";
import { DurableObject } from "cloudflare:workers";
import { now } from "./clock";
import type { Delivery, Env } from "./env";
import { ingest as ingestEvent, write } from "./events";
import { log } from "./log";
import {
  breakGlass,
  sameVerdict,
  verdictFor,
  type RevocationList,
  type Verdict as Revocation,
} from "./revocations";
import {
  advance,
  append,
  classify,
  masterRungs,
  measuredBandwidth,
  newRung,
  renderMaster,
  renderMedia,
  renderSlateLoop,
  RETAIN_MS,
  rungStatus,
  type Entry,
  type RungInfo,
  type RungState,
  type RungStatus,
  type SlateSegment,
  type Verdict,
} from "./playlist";

/** At most this many PUTs a second per channel and rung (§2). */
export const BURST_PER_SECOND = 4;
/** Slate segments a channel may hold (a short loop). */
export const MAX_SLATE = 8;
const ALARM_EVERY_MS = 60_000;
/** Each viewer is stored under this prefix and its session (ChannelLive.save). */
const VIEWER_KEY = "v:";
/** The most keys one storage put or delete takes. */
const STORAGE_BATCH = 128;
/** A channel re-reads the revocation list at most this often (a missed fan-out, a home entry). */
export const REVOCATION_CHECK_MS = 10_000;
/**
 * A woken on-demand channel shows `waiting` (tuning) until its first segment, for at most this
 * long (the target is 15 s); then its status is what its segments say.
 */
export const WAKE_WAIT_MS = 60_000;

export type ChannelStatus = "waiting" | "live" | "offline_slate" | "off_air" | "revoked";

/** The plan as the publisher's PublisherControl pushes it to its channels (50.20). */
export interface ChannelPlan {
  off: boolean;
  /** the mode a new viewer session gets (50.20 §2): `lowest` lets the home drop its top rungs */
  mode?: Mode;
  max_rungs?: number;
  max_top_kbps?: number;
  delivery?: Delivery | null;
}

/** A viewer as the channel keeps it: its last play, master or heartbeat, and its mode. */
interface Viewer {
  at: number;
  mode: "full" | "lowest";
}

interface State {
  ch: string;
  pub: string | null;
  home: string | null;
  registered: boolean;
  rungs: Record<string, RungState>;
  /** rung order as the ingest token lists them, top first */
  order: string[];
  info: RungInfo[];
  onAir: boolean;
  slate: SlateSegment[];
  /** replaced slate objects, deleted 10 minutes after they were replaced */
  retired: Record<string, number>;
  /** what the revocation list says of this channel (50.25) */
  verdict: Revocation | null;
  plan: ChannelPlan;
  stream: { uid: string; rtmps_url: string; stream_key: string } | null;
  /** the country rule of the newest ingest token (50.23 §5); null: none */
  geo?: Geo | null;
  /** that token's iat: an older token never replaces a newer one's rule */
  geoIat?: number;
  counters: { segments_in: number; bytes_in: number; playlist_misses: number };
  /** the home's mode, named on its control socket's connect: on demand wakes and sleeps */
  onDemand?: boolean;
  /** the channel's viewers: session → last seen (play, master, heartbeat) and its mode */
  viewers?: Record<string, Viewer>;
  /** on demand: when `wake` was sent; null or absent: asleep */
  wokeAt?: number | null;
  /** the rungs last sent in `limits` ([] or absent: all) */
  limits?: string[];
}

export interface PrecheckInput {
  pub: string;
  home: string;
  rung: string;
  rungs: string[];
  seq: number;
  hash8: string;
  bytes: number;
  dur: number;
  /** the ingest token's country rule (50.23 §5) */
  geo?: Geo | null;
  /** the ingest token's iat */
  iat?: number;
}

export type PrecheckVerdict =
  | Verdict
  | "rate_limited"
  | "revoked"
  | "plan_off"
  | "plan_rungs"
  | "too_large"
  | "wrong_publisher";

export interface AppendInput extends PrecheckInput {
  key: string;
  disc: boolean;
  pdt: number;
  /** edge time the upload started (50.28's upload-to-visible) */
  recv?: number;
}

export type PlaylistAnswer =
  | { kind: "playlist"; body: string }
  | { kind: "stream"; uid: string | null }
  | { kind: "error"; status: number; error: string; reason?: string };

function initial(): State {
  return {
    ch: "",
    pub: null,
    home: null,
    registered: false,
    rungs: {},
    order: [],
    info: [],
    onAir: true,
    slate: [],
    retired: {},
    verdict: null,
    plan: { off: false },
    stream: null,
    counters: { segments_in: 0, bytes_in: 0, playlist_misses: 0 },
  };
}

export class ChannelLive extends DurableObject<Env> {
  private s: State = initial();
  /** recent PUT times per rung (memory only: an eviction forgets a second's worth) */
  private burst = new Map<string, number[]>();
  /** the revocation list as last handed over or read (memory only: re-read after an eviction) */
  private revs: { at: number; list: RevocationList } | null = null;
  /** the sessions whose viewer key is to be written (or deleted, once gone) by the next save */
  private viewersDirty = new Set<string>();

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    ctx.blockConcurrencyWhile(async () => {
      const saved = await ctx.storage.get<State>("s");
      if (saved) this.s = { ...initial(), ...saved, verdict: saved.verdict ?? null };
      const viewers: NonNullable<State["viewers"]> = { ...(saved?.viewers ?? {}) };
      for (const [k, v] of await ctx.storage.list<Viewer>({ prefix: VIEWER_KEY }))
        viewers[k.slice(VIEWER_KEY.length)] = v;
      this.s.viewers = viewers;
      // the earlier layout kept the viewers inside "s": each moves to its own key once
      if (saved?.viewers) {
        for (const k of Object.keys(saved.viewers)) this.viewersDirty.add(k);
        await this.save();
      }
    });
  }

  /**
   * save writes the channel's state ("s", unless only the viewers changed) and the viewers
   * written or gone since the last save, each under its own key: a heartbeat never rewrites
   * every viewer and every segment (one write per beat over the whole state was O(viewers) a
   * beat, and stalled the channel's ingest at 1,000 viewers).
   */
  private async save(state = true): Promise<void> {
    const { viewers, ...rest } = this.s;
    const put: [string, unknown][] = state ? [["s", rest]] : [];
    const gone: string[] = [];
    for (const id of this.viewersDirty) {
      const v = viewers?.[id];
      if (v) put.push([VIEWER_KEY + id, v]);
      else gone.push(VIEWER_KEY + id);
    }
    this.viewersDirty.clear();
    const writes: Promise<unknown>[] = [];
    for (let i = 0; i < put.length; i += STORAGE_BATCH)
      writes.push(this.ctx.storage.put(Object.fromEntries(put.slice(i, i + STORAGE_BATCH))));
    for (let i = 0; i < gone.length; i += STORAGE_BATCH)
      writes.push(this.ctx.storage.delete(gone.slice(i, i + STORAGE_BATCH)));
    await Promise.all(writes);
  }

  private async ensureAlarm(): Promise<void> {
    if ((await this.ctx.storage.getAlarm()) === null) {
      await this.ctx.storage.setAlarm(Date.now() + ALARM_EVERY_MS);
    }
  }

  private rung(name: string): RungState {
    let r = this.s.rungs[name];
    if (!r) {
      r = newRung();
      this.s.rungs[name] = r;
    }
    return r;
  }

  status(at: number = now()): ChannelStatus {
    if (this.s.verdict?.stop === "off_air") return "revoked";
    // on demand: asleep, or woken and tuning, until the first segment (or WAKE_WAIT_MS)
    if (this.s.onDemand && (this.s.wokeAt == null || this.tuning(at))) return "waiting";
    if (!this.s.onAir) return "off_air";
    const all = Object.values(this.s.rungs).map((r) => rungStatus(r, at));
    if (all.length === 0) return "waiting";
    const has = (x: RungStatus) => all.includes(x);
    if (has("live")) return "live";
    if (has("offline_slate")) return "offline_slate";
    if (all.every((x) => x === "off_air")) return "off_air";
    return "waiting";
  }

  /** tuning: woken, no segment since, and not yet WAKE_WAIT_MS ago. */
  private tuning(at: number): boolean {
    const w = this.s.wokeAt;
    if (w == null || at - w >= WAKE_WAIT_MS) return false;
    return !Object.values(this.s.rungs).some((r) => r.lastSeq !== null && r.lastAt >= w);
  }

  // ---- demand: viewers, wake, sleep, limits (50.22 §2, 50.26) -------------------------------

  /**
   * viewer records a session's play, master playlist or heartbeat; the first viewer of an
   * asleep on-demand channel wakes the home. It answers the channel's status (`waiting` while
   * the home starts).
   */
  async viewer(session: string, mode: Mode): Promise<ChannelStatus> {
    const t = now();
    if (session && (mode === "full" || mode === "lowest")) {
      const viewers = (this.s.viewers ??= {});
      const known = viewers[session];
      viewers[session] = { at: t, mode };
      this.viewersDirty.add(session);
      // a watching session's next heartbeat changes nothing the home hears: it is written with
      // the next save (the alarm saves every ALARM_EVERY_MS, well inside SESSION_TTL_MS). A new
      // session, a changed mode or a lapsed one may wake the home or change its limits.
      if (!known || known.mode !== mode || t - known.at > SESSION_TTL_MS) {
        await this.save(this.demand(t));
        await this.ensureAlarm();
      }
    }
    return this.status(t);
  }

  /** pruneViewers drops the viewers with no heartbeat for SESSION_TTL_MS. */
  private pruneViewers(t: number): void {
    const viewers = this.s.viewers;
    if (!viewers) return;
    for (const [k, v] of Object.entries(viewers))
      if (t - v.at > SESSION_TTL_MS) {
        delete viewers[k];
        this.viewersDirty.add(k);
      }
  }

  /**
   * demand settles what the home is told: an on-demand channel with a viewer is woken, one with
   * none is put to sleep, and the limits follow the plan and the viewers. True when it changed.
   */
  private demand(t: number): boolean {
    const before = JSON.stringify([this.s.wokeAt ?? null, this.s.limits]);
    this.pruneViewers(t);
    const watched = Object.keys(this.s.viewers ?? {}).length > 0;
    if (!this.s.onDemand) {
      this.s.wokeAt = null;
    } else if (watched && this.s.wokeAt == null) {
      this.s.wokeAt = t;
      this.notify({ type: "wake" });
    } else if (!watched && this.s.wokeAt != null) {
      this.s.wokeAt = null;
      this.notify({ type: "sleep" });
    }
    const want = this.wantLimits();
    if (JSON.stringify(want) !== JSON.stringify(this.s.limits ?? [])) {
      this.s.limits = want;
      this.notify({ type: "limits", rungs: want });
    }
    // the viewers are their own keys (save writes the dirty ones); this is what "s" holds
    return before !== JSON.stringify([this.s.wokeAt ?? null, this.s.limits]);
  }

  /**
   * wantLimits is the rungs the home should send, top first, or [] for all: the plan's rung
   * count, and in `lowest` with no `full` viewer left (a session keeps its mode, 50.26), only
   * the lowest of those.
   */
  private wantLimits(): string[] {
    const order = this.s.order;
    if (order.length === 0) return [];
    const max = this.s.plan.max_rungs;
    let allowed = max !== undefined ? order.slice(0, Math.max(1, max)) : order.slice();
    const full = Object.values(this.s.viewers ?? {}).some((v) => v.mode === "full");
    if (this.s.plan.mode === "lowest" && !full) allowed = allowed.slice(-1);
    return allowed.length < order.length ? allowed : [];
  }

  /**
   * noteControl takes what the home's control socket connect says: its mode (X-Maktaba-Mode)
   * and the ingest token's rungs, top first.
   */
  async noteControl(mode: string, rungs: string[]): Promise<void> {
    const onDemand = mode === "on_demand";
    if (!onDemand) this.s.wokeAt = null;
    this.s.onDemand = onDemand;
    if (rungs.length > 0) this.s.order = rungs;
    await this.save();
  }

  /** precheck answers before the Worker writes anything to R2 (§2's guards and retries). */
  async precheck(inp: PrecheckInput): Promise<PrecheckVerdict> {
    // a channel not yet appended to knows its publisher and home from this request only
    await this.checkRevocations(now(), { pub: inp.pub, home: inp.home });
    if (this.s.verdict?.stop === "off_air") return "revoked";
    if (this.s.plan.off) return "plan_off";
    if (this.s.pub && this.s.pub !== inp.pub) return "wrong_publisher";
    const t = now();
    const recent = (this.burst.get(inp.rung) ?? []).filter((x) => t - x < 1000);
    recent.push(t);
    this.burst.set(inp.rung, recent);
    if (recent.length > BURST_PER_SECOND) return "rate_limited";
    const max = this.s.plan.max_rungs;
    if (max !== undefined && inp.rungs.indexOf(inp.rung) >= max) return "plan_rungs";
    const kbps = this.s.plan.max_top_kbps;
    if (kbps !== undefined && inp.bytes > maxSegmentBytes(inp.dur, kbps)) return "too_large";
    return classify(this.rung(inp.rung), inp.seq, inp.hash8);
  }

  /** append records a segment the Worker wrote to R2 (re-checked: a race is a conflict). */
  async append(inp: AppendInput): Promise<Verdict> {
    if (!this.s.ch) this.s.ch = channelOfKey(inp.key);
    if (inp.rungs.length > 0) this.s.order = inp.rungs;
    const r = this.rung(inp.rung);
    const t = now();
    const before = r.nextN;
    const v = append(
      r,
      {
        seq: inp.seq,
        hash8: inp.hash8,
        key: inp.key,
        dur: inp.dur,
        bytes: inp.bytes,
        disc: inp.disc,
        pdt: inp.pdt,
        ...(inp.recv !== undefined ? { recv: inp.recv } : {}),
      },
      t
    );
    if (v !== "new") return v;
    this.recordVisible(
      inp.pub,
      inp.rung,
      r.entries.filter((e) => e.n >= before),
      t
    );
    const resumed = !this.s.onAir;
    this.s.onAir = true;
    this.s.pub ??= inp.pub;
    this.s.home = inp.home;
    this.takeGeo(inp.geo ?? null, inp.iat ?? 0);
    this.s.counters.segments_in++;
    this.s.counters.bytes_in += inp.bytes;
    const registerWith = this.s.registered ? null : this.s.pub;
    this.s.registered = true;
    await this.save();
    await this.ensureAlarm();
    if (registerWith) {
      await this.env.PUBLISHER.get(this.env.PUBLISHER.idFromName(registerWith)).addChannel(
        this.s.ch
      );
    }
    if (resumed) this.notify({ type: "on_air" });
    return v;
  }

  /** putSlate stores one segment of the channel's offline slate loop (uploaded once per publish). */
  async putSlate(seg: SlateSegment, ch: string, pub: string): Promise<"created" | "duplicate"> {
    if (!this.s.ch) this.s.ch = ch;
    this.s.pub ??= pub;
    const old = this.s.slate.find((x) => x.idx === seg.idx);
    if (old && old.hash8 === seg.hash8) return "duplicate";
    if (old) this.s.retired[old.key] = now();
    this.s.slate = [...this.s.slate.filter((x) => x.idx !== seg.idx), seg].sort(
      (a, b) => a.idx - b.idx
    );
    await this.save();
    await this.ensureAlarm();
    return "created";
  }

  /** setState takes the home's state post: the rungs' attributes, and whether it is on air. */
  async setState(
    post: { rungs?: RungInfo[]; on_air?: boolean },
    ch: string
  ): Promise<ChannelStatus> {
    if (!this.s.ch) this.s.ch = ch;
    if (post.rungs) this.s.info = post.rungs;
    if (post.on_air === false && this.s.onAir) {
      this.s.onAir = false;
      this.notify({ type: "off_air" });
    } else if (post.on_air === true) {
      this.s.onAir = true;
    }
    await this.save();
    return this.status();
  }

  async setPlan(plan: ChannelPlan): Promise<void> {
    this.s.plan = plan;
    this.demand(now());
    await this.save();
    if (plan.off) this.notify({ type: "plan_off" });
  }

  /** geo is the channel's country rule (50.23 §5), as its newest ingest token carried it. */
  async geo(): Promise<Geo | null> {
    return this.s.geo ?? null;
  }

  /**
   * noteGeo records the rule of a token-authenticated ingest call that appends no segment (the
   * slate, the state post, the Stream input, the control socket), so a channel fed only that
   * way still carries its rule. A token older than the one the rule came from is ignored.
   */
  async noteGeo(geo: Geo | null, iat: number): Promise<void> {
    if (this.takeGeo(geo, iat)) await this.save();
  }

  private takeGeo(geo: Geo | null, iat: number): boolean {
    if (iat < (this.s.geoIat ?? 0)) return false;
    const changed =
      JSON.stringify(geo) !== JSON.stringify(this.s.geo ?? null) || iat !== this.s.geoIat;
    this.s.geo = geo;
    this.s.geoIat = iat;
    return changed;
  }

  /** applyList takes a newly installed revocation list (the Revocations fan-out). */
  async applyList(list: RevocationList, ch: string): Promise<Revocation | null> {
    if (!this.s.ch) this.s.ch = ch;
    this.revs = { at: now(), list };
    await this.reverdict(now());
    return this.s.verdict;
  }

  /** checkRevocations re-reads the list when the copy is 10 s old (or missing). */
  private async checkRevocations(
    t: number,
    who: { pub?: string; home?: string } = {}
  ): Promise<void> {
    if (!this.revs || t - this.revs.at >= REVOCATION_CHECK_MS) {
      try {
        const list = await this.env.REVOCATIONS.get(
          this.env.REVOCATIONS.idFromName("global")
        ).current();
        this.revs = { at: t, list };
      } catch (err) {
        // keep the verdict held: a stop stays a stop while the list can't be read
        log("warn", "revocation list unreadable", {
          component: "channel",
          channel: this.s.ch,
          err,
        });
        return;
      }
    }
    await this.reverdict(t, who);
  }

  /** reverdict applies the list to this channel and tells the home when the answer changed. */
  private async reverdict(t: number, who: { pub?: string; home?: string } = {}): Promise<void> {
    if (!this.revs) return;
    const v = verdictFor(
      this.revs.list,
      { ch: this.s.ch, pub: this.s.pub ?? who.pub, home: this.s.home ?? who.home },
      t
    );
    if (sameVerdict(v, this.s.verdict)) return;
    this.s.verdict = v;
    await this.save();
    if (v?.stop === "off_air") this.notify({ type: "revoked", action: v.action, reason: v.reason });
    else if (v?.stop === "slate")
      this.notify({ type: "slate", reason: v.reason, until: new Date(v.until).toISOString() });
    else this.notify({ type: "restored" });
  }

  /** delivery is this channel's: the publisher's override, else the global var. */
  private delivery(): Delivery {
    const d = this.s.plan.delivery;
    if (d === "stream" || d === "r2") return d;
    return this.env.BROADCAST_DELIVERY === "stream" ? "stream" : "r2";
  }

  private async refusal(at: number): Promise<PlaylistAnswer | null> {
    const glass = breakGlass(this.env, this.s.ch);
    if (glass) return { kind: "error", status: 503, error: "off_air", reason: glass.reason };
    await this.checkRevocations(at);
    const v = this.s.verdict;
    if (v?.stop === "off_air")
      return { kind: "error", status: 503, error: "off_air", reason: v.reason };
    if (this.s.plan.off) return { kind: "error", status: 403, error: "broadcast_off" };
    if (this.status(at) === "off_air") return { kind: "error", status: 503, error: "off_air" };
    return null;
  }

  /**
   * media answers a rung's playlist for a session's mode (the Worker caches it 1 s per location,
   * per rung and mode). A rung the mode or the plan does not allow is 403: a `lowest` session
   * gets only the rung its master playlist lists, whatever URL it asks for.
   */
  async media(rung: string, mode: "full" | "lowest" = "full"): Promise<PlaylistAnswer> {
    const t = now();
    this.s.counters.playlist_misses++;
    const no = await this.refusal(t);
    if (no) return no;
    const slate = this.programmeSlate(t);
    if (slate) return slate;
    if (this.delivery() === "stream") return { kind: "stream", uid: this.s.stream?.uid ?? null };
    const r = this.s.rungs[rung];
    if (!r) return { kind: "error", status: 404, error: "no_such_rung" };
    if (!this.allowedRungs(mode).includes(rung))
      return { kind: "error", status: 403, error: "rung_not_allowed" };
    if (this.step(rung, r, t)) await this.save();
    const body = renderMedia(r);
    if (!body) return { kind: "error", status: 404, error: "not_live" };
    return { kind: "playlist", body };
  }

  /** master answers the master playlist for a session's mode. */
  async master(mode: "full" | "lowest"): Promise<PlaylistAnswer> {
    const t = now();
    this.s.counters.playlist_misses++;
    const no = await this.refusal(t);
    if (no) return no;
    if (this.delivery() === "stream" && !this.programmeSlate(t))
      return { kind: "stream", uid: this.s.stream?.uid ?? null };
    // no state post yet: the rungs seen, top first, with their measured bandwidth
    const info = this.rungInfo();
    const list = masterRungs(info, mode, this.planRungs());
    if (list.length === 0) return { kind: "error", status: 404, error: "not_live" };
    return { kind: "playlist", body: renderMaster(list) };
  }

  /**
   * programmeSlate is the slate loop while a programme entry holds (50.25 §1), every rung the
   * same; with no slate uploaded the channel is off air until the entry ends. Null otherwise.
   */
  private programmeSlate(t: number): PlaylistAnswer | null {
    const v = this.s.verdict;
    if (v?.stop !== "slate" || t >= v.until) return null;
    const since = Date.parse(
      this.revs?.list.entries.find((e) => e.id === v.id)?.since ?? new Date(t).toISOString()
    );
    const body = renderSlateLoop(this.s.slate, Number.isFinite(since) ? since : t, t);
    if (!body) return { kind: "error", status: 503, error: "off_air", reason: v.reason };
    return { kind: "playlist", body };
  }

  /** planRungs is the rungs the plan allows (top first), or undefined for all. */
  private planRungs(): string[] | undefined {
    if (this.s.plan.max_rungs === undefined) return undefined;
    const allowed = this.s.order.slice(0, this.s.plan.max_rungs);
    return allowed.length > 0 ? allowed : undefined;
  }

  /** rungInfo is the rungs' attributes: the state post's, else measured from the segments. */
  private rungInfo(): RungInfo[] {
    const info = this.s.info.filter((x) => this.s.rungs[x.name]);
    if (info.length > 0) return info;
    const names = [...this.s.order.filter((n) => this.s.rungs[n]), ...Object.keys(this.s.rungs)];
    return [...new Set(names)].map((name) => ({
      name,
      bandwidth: measuredBandwidth(this.s.rungs[name]!) || 1,
    }));
  }

  /** allowedRungs is what a session's mode may play: the rungs its master playlist lists. */
  private allowedRungs(mode: "full" | "lowest"): string[] {
    const listed = masterRungs(this.rungInfo(), mode, this.planRungs()).map((r) => r.name);
    // a rung with segments but no state-post entry yet stays playable in full mode
    if (mode === "full") {
      const plan = this.planRungs();
      for (const name of Object.keys(this.s.rungs))
        if (!listed.includes(name) && (!plan || plan.includes(name))) listed.push(name);
    }
    return listed;
  }

  /**
   * streamFor answers a Stream redirect's checks (BROADCAST_DELIVERY = stream): the same refusals
   * as a playlist (revoked, the plan off, off air), else the channel's live input.
   */
  async streamFor(): Promise<PlaylistAnswer> {
    const no = await this.refusal(now());
    if (no) return no;
    return { kind: "stream", uid: this.s.stream?.uid ?? null };
  }

  /** streamRefusal is why the home may not have a Stream live input now, or null. */
  async streamRefusal(): Promise<string | null> {
    await this.checkRevocations(now());
    if (this.s.verdict?.stop === "off_air") return "revoked";
    if (this.s.plan.off) return "broadcast_off";
    return null;
  }

  /** step advances one rung and tells the home when it stalls. True when it changed. */
  private step(name: string, r: RungState, t: number): boolean {
    const before = r.nextN;
    const events = advance(r, t, this.s.slate);
    if (events.includes("stalled")) this.notify({ type: "rung_stalled", rung: name });
    return events.length > 0 || r.nextN !== before;
  }

  /** stream is the channel's Stream live input (§5), or null before one was made. */
  async streamInput(): Promise<State["stream"]> {
    return this.s.stream;
  }

  async setStreamInput(input: NonNullable<State["stream"]>): Promise<void> {
    this.s.stream = input;
    await this.save();
  }

  /** snapshot is the channel's state for the tests and the operator's tools. */
  async snapshot(): Promise<{
    status: ChannelStatus;
    pub: string | null;
    rungs: Record<string, RungState>;
    slate: SlateSegment[];
    verdict: Revocation | null;
  }> {
    return {
      status: this.status(),
      pub: this.s.pub,
      rungs: this.s.rungs,
      slate: this.s.slate,
      verdict: this.s.verdict,
    };
  }

  // ---- the alarm: playlists move on, old objects go, counts are reported -------------------

  override async alarm(): Promise<void> {
    const t = now();
    for (const [name, r] of Object.entries(this.s.rungs)) this.step(name, r, t);
    // the last viewer gone: an on-demand home is put to sleep; the limits follow the viewers
    this.demand(t);
    await this.sweep(t);
    await this.report();
    await this.save();
    const busy =
      Object.keys(this.s.viewers ?? {}).length > 0 ||
      this.s.wokeAt != null ||
      Object.values(this.s.rungs).some((r) => Object.keys(r.written).length > 0) ||
      Object.keys(this.s.retired).length > 0 ||
      this.status(t) === "live" ||
      this.status(t) === "offline_slate";
    if (busy) await this.ctx.storage.setAlarm(Date.now() + ALARM_EVERY_MS);
  }

  /** sweep deletes segment objects older than 10 minutes (deletes are free; no DVR, Q19). */
  async sweep(t: number = now()): Promise<number> {
    const doomed: string[] = [];
    const inPlaylist = new Set<string>();
    for (const r of Object.values(this.s.rungs)) {
      for (const e of r.entries) if (e.kind === "live") inPlaylist.add(e.uri);
      for (const [key, at] of Object.entries(r.written)) {
        if (t - at > RETAIN_MS && !inPlaylist.has(key.slice(key.lastIndexOf("/") + 1))) {
          doomed.push(key);
          delete r.written[key];
        }
      }
    }
    for (const [key, at] of Object.entries(this.s.retired)) {
      if (t - at > RETAIN_MS) {
        doomed.push(key);
        delete this.s.retired[key];
      }
    }
    // the backstop: objects this channel lost track of (a write whose append never came)
    if (this.s.ch) {
      const keep = new Set([...this.s.slate.map((x) => x.key), ...Object.keys(this.s.retired)]);
      for (const r of Object.values(this.s.rungs))
        for (const k of Object.keys(r.written)) keep.add(k);
      let cursor: string | undefined;
      do {
        const page = await this.env.SEGMENTS.list({
          prefix: `${this.env.BROADCAST_ENV}/${this.s.ch}/`,
          cursor,
        });
        for (const o of page.objects) {
          if (!keep.has(o.key) && !doomed.includes(o.key) && t - o.uploaded.getTime() > RETAIN_MS)
            doomed.push(o.key);
        }
        cursor = page.truncated ? page.cursor : undefined;
      } while (cursor);
    }
    for (let i = 0; i < doomed.length; i += 1000)
      await this.env.SEGMENTS.delete(doomed.slice(i, i + 1000));
    if (doomed.length > 0)
      log("debug", "segments deleted", {
        component: "channel",
        channel: this.s.ch,
        attrs: { count: doomed.length },
      });
    return doomed.length;
  }

  /**
   * recordVisible writes 50.28's `ingest` event for each live segment an append made visible:
   * its bytes, upload-to-visible, and for the canary publisher glass-to-glass (now minus the
   * segment's PROGRAM-DATE-TIME: when its first frame reached the home's broadcaster, on the
   * home's clock).
   */
  private recordVisible(pub: string, rung: string, visible: Entry[], t: number): void {
    const canary = !!this.env.CANARY_PUBLISHER && pub === this.env.CANARY_PUBLISHER;
    for (const e of visible) {
      if (e.kind !== "live") continue;
      write(
        this.env,
        ingestEvent(
          pub,
          this.s.ch,
          rung,
          e.bytes,
          e.recv !== undefined ? Math.max(0, t - e.recv) : 0,
          canary ? Math.max(0, t - e.pdt) : undefined
        )
      );
    }
  }

  /** report sends the last minute's ingest counts to the publisher (50.20). */
  private async report(): Promise<void> {
    const c = this.s.counters;
    if (c.segments_in === 0 && c.bytes_in === 0 && c.playlist_misses === 0) return;
    if (this.s.pub && (c.segments_in > 0 || c.bytes_in > 0)) {
      try {
        await this.env.PUBLISHER.get(this.env.PUBLISHER.idFromName(this.s.pub)).recordIngest(
          c.segments_in,
          c.bytes_in
        );
      } catch (err) {
        log("warn", "publisher count not recorded", {
          component: "channel",
          channel: this.s.ch,
          err,
        });
        return; // kept for the next minute
      }
    }
    this.s.counters = { segments_in: 0, bytes_in: 0, playlist_misses: 0 };
  }

  // ---- the home's control WebSocket (hibernating) ------------------------------------------

  override async fetch(request: Request): Promise<Response> {
    if (request.headers.get("Upgrade") !== "websocket")
      return new Response("not found", { status: 404 });
    const pair = new WebSocketPair();
    const [client, server] = [pair[0], pair[1]];
    this.ctx.acceptWebSocket(server);
    const t = now();
    server.send(JSON.stringify({ type: "hello", status: this.status(t), at: t }));
    // a (re)connected home hears what it should be doing now: woken while viewers watch, and
    // the current limits (it may have restarted and forgotten both)
    const wokeBefore = this.s.wokeAt ?? null;
    const limitsBefore = JSON.stringify(this.s.limits ?? []);
    const changed = this.demand(t); // a wake or limits it sends reaches this socket too
    if (this.s.onDemand && this.s.wokeAt != null && wokeBefore != null)
      server.send(JSON.stringify({ type: "wake", at: t }));
    const lim = this.s.limits ?? [];
    if (lim.length > 0 && JSON.stringify(lim) === limitsBefore)
      server.send(JSON.stringify({ type: "limits", rungs: lim, at: t }));
    if (changed || this.viewersDirty.size > 0) await this.save(changed);
    if (this.s.wokeAt != null || Object.keys(this.s.viewers ?? {}).length > 0)
      await this.ensureAlarm();
    return new Response(null, { status: 101, webSocket: client });
  }

  override async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (message === "ping") ws.send(JSON.stringify({ type: "pong", at: now() }));
  }

  override async webSocketClose(ws: WebSocket, code: number): Promise<void> {
    try {
      ws.close(code, "closed");
    } catch {
      // already closed
    }
  }

  private notify(msg: Record<string, unknown>): void {
    const text = JSON.stringify({ ...msg, at: now() });
    for (const ws of this.ctx.getWebSockets()) {
      try {
        ws.send(text);
      } catch {
        // a socket that went away: the home reconnects
      }
    }
  }
}

/** maxSegmentBytes is the size guard: duration × top kbps × 1.5 (§2). */
export function maxSegmentBytes(durS: number, kbps: number): number {
  return Math.ceil(((durS * kbps * 1000) / 8) * 1.5);
}

function channelOfKey(key: string): string {
  return key.split("/")[1] ?? "";
}
