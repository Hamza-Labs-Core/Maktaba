// ViewerAccess (story 50.26): one Durable Object per publisher, beside its PublisherControl. It
// holds what the cloud says about the publisher's viewer access (the "access" control post:
// channels and their access, limits, revoked invites and passes, short codes), counts each
// invite's devices, and admits, beats and ends viewer sessions under the limits:
//
//   concurrent viewers per publisher (the plan's, from PublisherControl) and per channel,
//   open sessions per network (a salted hash; a guest pass never counts), daily minutes per
//   guest pass or open session, short-code tries.
//
// A limit only refuses NEW sessions: a session already playing keeps its mode and is never
// stopped by one. Revoking an invite or a pass ends its sessions at their next beat; a session
// with no beat for 150 s ends and gets no new token.
import { DurableObject } from "cloudflare:workers";
import { now } from "./clock";
import type { Env } from "./env";
import { json, problem } from "./http";
import { sameText } from "./crypto";
import { randomId, type InviteClaims } from "./invites";
import {
  CODE_FAILS_PER_PUB,
  CODE_TRIES_PER_IP,
  CODE_WINDOW_MS,
  DEFAULT_SESSIONS_PER_IP,
  MINUTE_GAP_MS,
  SESSION_TTL_MS,
} from "./limits";
import { verifyControl, type Mode } from "./tokens";

export interface AccessChannel {
  access: "invite" | "open";
  max_viewers: number;
  indexable: boolean;
}

/** The cloud's access post (cloud/internal/broadcast.AccessState). */
export interface AccessSettings {
  pub: string;
  version: number;
  channels: Record<string, AccessChannel>;
  open_allowed: boolean;
  max_sessions_per_ip: number;
  daily_minutes: number;
  revoked_invites: string[];
  revoked_passes: string[];
  codes: Record<string, string>;
}

interface Pass {
  id: string;
  inv: string;
  first: number;
  last: number;
}

interface Session {
  ch: string;
  /** the last beat (or the play) */
  at: number;
  /** the mode the session was admitted with: kept for its whole life */
  mode: Mode;
  /** the credential: a guest pass's id (with its invite), or an open session's id */
  pass?: string;
  inv?: string;
  open?: string;
  /** the network's salted hash, for open sessions only */
  ip?: string;
  /** when the last minute was counted */
  counted: number;
  /** the current viewer token's expiry (ms) */
  tokenExp: number;
  /**
   * the beat key: given only in the play answer, never in a URL (the session id is readable in
   * the viewer token's payload, so it alone must not keep a session alive or mint a token)
   */
  key: string;
}

interface Daily {
  day: string;
  minutes: number;
}

interface State {
  pub: string;
  settings: AccessSettings | null;
  passes: Record<string, Pass>;
  sessions: Record<string, Session>;
  daily: Record<string, Daily>;
  tries: Record<string, number[]>;
  fails: number[];
  /** the attestation challenges already used, until they expire: nonce → expiry (ms) */
  nonces?: Record<string, number>;
}

export type Cred =
  | { kind: "pass"; id: string; inv: string; chs: string[] }
  | { kind: "open"; id: string }
  | { kind: "none" };

export type Refusal = { ok: false; status: number; error: string };

export type RedeemResult = { ok: true; id: string; reused: boolean } | Refusal;
export type PlayResult =
  | { ok: true; session: string; key: string; mode: Mode; channels: string[] }
  | Refusal;
export type BeatResult =
  | { ok: true; ch: string; mode: Mode; refresh: boolean; concurrent: number }
  | Refusal;

export interface PlanView {
  mode: Mode;
  max_viewers?: number;
}

/** The spent attestation challenges one instance keeps (5 minutes' worth at the rate limits). */
export const MAX_SPENT = 50_000;

function day(t: number): string {
  return new Date(t).toISOString().slice(0, 10);
}

export class ViewerAccess extends DurableObject<Env> {
  private s: State = {
    pub: "",
    settings: null,
    passes: {},
    sessions: {},
    daily: {},
    tries: {},
    fails: [],
  };

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    ctx.blockConcurrencyWhile(async () => {
      const saved = await ctx.storage.get<State>("s");
      if (saved) this.s = saved;
    });
  }

  private async save(): Promise<void> {
    await this.ctx.storage.put("s", this.s);
  }

  /** prune drops ended sessions, old tries and old days. */
  private prune(t: number): void {
    for (const [k, v] of Object.entries(this.s.sessions))
      if (t - v.at > SESSION_TTL_MS) delete this.s.sessions[k];
    for (const [k, v] of Object.entries(this.s.tries)) {
      const kept = v.filter((x) => t - x < CODE_WINDOW_MS);
      if (kept.length) this.s.tries[k] = kept;
      else delete this.s.tries[k];
    }
    this.s.fails = this.s.fails.filter((x) => t - x < CODE_WINDOW_MS);
    const today = day(t);
    for (const [k, v] of Object.entries(this.s.daily)) if (v.day !== today) delete this.s.daily[k];
  }

  /** install takes the cloud's access post; an older version never replaces a newer. */
  async install(a: AccessSettings): Promise<"applied" | "stale"> {
    if (this.s.settings && a.version < this.s.settings.version) return "stale";
    this.s.pub ||= a.pub;
    this.s.settings = a;
    await this.save();
    return "applied";
  }

  async settings(): Promise<AccessSettings | null> {
    return this.s.settings;
  }

  private revoked(inv?: string, pass?: string): boolean {
    const st = this.s.settings;
    if (!st) return false;
    return (
      (!!inv && st.revoked_invites.includes(inv)) || (!!pass && st.revoked_passes.includes(pass))
    );
  }

  /**
   * redeem counts one device's use of an invite (already checked: signature, expiry). A device
   * that already holds a live pass of the same invite reuses it and uses nothing.
   */
  async redeem(c: InviteClaims, held?: string): Promise<RedeemResult> {
    const t = now();
    this.s.pub ||= c.pub;
    if (this.revoked(c.inv)) return { ok: false, status: 410, error: "invite_revoked" };
    if (held && this.s.passes[held]?.inv === c.inv && !this.revoked(c.inv, held)) {
      this.s.passes[held]!.last = t;
      await this.save();
      return { ok: true, id: held, reused: true };
    }
    const used = Object.values(this.s.passes).filter((p) => p.inv === c.inv).length;
    if (used >= c.max) return { ok: false, status: 410, error: "invite_used" };
    const id = randomId();
    this.s.passes[id] = { id, inv: c.inv, first: t, last: t };
    await this.save();
    return { ok: true, id, reused: false };
  }

  /**
   * code maps a short code to its invite token: 10 tries a minute per network, and at most 60
   * failed tries a minute for the publisher from all networks together.
   */
  async code(code: string, ip: string): Promise<{ ok: true; token: string } | Refusal> {
    const t = now();
    this.prune(t);
    const tries = (this.s.tries[ip] ??= []);
    if (tries.length >= CODE_TRIES_PER_IP || this.s.fails.length >= CODE_FAILS_PER_PUB) {
      await this.save();
      return { ok: false, status: 429, error: "rate_limited" };
    }
    tries.push(t);
    const token = this.s.settings?.codes[code];
    if (!token) {
      this.s.fails.push(t);
      await this.save();
      return { ok: false, status: 404, error: "code_unknown" };
    }
    await this.save();
    return { ok: true, token };
  }

  /** channelsFor lists the channels a credential opens (for the neighbours). */
  private channelsFor(cred: Cred): string[] {
    const st = this.s.settings;
    if (!st) return [];
    return Object.entries(st.channels)
      .filter(([ch, c]) => {
        if (cred.kind === "pass") return cred.chs.length === 0 || cred.chs.includes(ch);
        if (cred.kind === "open") return c.access === "open" && st.open_allowed;
        return false;
      })
      .map(([ch]) => ch)
      .sort();
  }

  /** play admits a new session on ch, or says which rule refused it. */
  async play(ch: string, cred: Cred, ip: string, plan: PlanView | null): Promise<PlayResult> {
    const t = now();
    this.prune(t);
    const st = this.s.settings;
    const c = st?.channels[ch];
    if (!st || !c) return { ok: false, status: 404, error: "channel_unknown" };
    const open = c.access === "open" && st.open_allowed;
    // access
    if (cred.kind === "none") {
      return open
        ? { ok: false, status: 403, error: "turnstile_required" }
        : { ok: false, status: 401, error: "invite_required" };
    }
    if (cred.kind === "pass") {
      if (this.revoked(cred.inv, cred.id) || !this.s.passes[cred.id])
        return { ok: false, status: 403, error: "pass_revoked" };
      if (cred.chs.length > 0 && !cred.chs.includes(ch))
        return { ok: false, status: 403, error: "not_invited" };
    }
    if (cred.kind === "open" && !open) return { ok: false, status: 403, error: "invite_required" };
    // the plan's mode for new viewers (50.20 §2)
    if (!plan) return { ok: false, status: 503, error: "plan_unknown" };
    if (plan.mode === "off") return { ok: false, status: 403, error: "broadcast_off" };
    if (plan.mode === "wait") return { ok: false, status: 429, error: "broadcast_wait" };
    // limits: new sessions only
    // one live session per guest pass or open session (one device): a new play from the same
    // credential replaces its own earlier session, and one credential never holds more
    const mine = (x: Session) =>
      cred.kind === "pass" ? x.pass === cred.id : x.open !== undefined && x.open === cred.id;
    const live = Object.values(this.s.sessions).filter((x) => !mine(x));
    if (plan.max_viewers !== undefined && live.length >= plan.max_viewers)
      return { ok: false, status: 429, error: "broadcast_full" };
    if (c.max_viewers > 0 && live.filter((x) => x.ch === ch).length >= c.max_viewers)
      return { ok: false, status: 429, error: "broadcast_full" };
    if (cred.kind === "open") {
      const most = st.max_sessions_per_ip > 0 ? st.max_sessions_per_ip : DEFAULT_SESSIONS_PER_IP;
      if (live.filter((x) => x.ip === ip).length >= most)
        return { ok: false, status: 429, error: "broadcast_ip_limit" };
    }
    const credId = cred.kind === "pass" ? `p:${cred.id}` : `o:${cred.id}`;
    if (st.daily_minutes > 0 && (this.s.daily[credId]?.minutes ?? 0) >= st.daily_minutes)
      return { ok: false, status: 429, error: "broadcast_daily_limit" };
    for (const [k, x] of Object.entries(this.s.sessions)) if (mine(x)) delete this.s.sessions[k];
    const session = randomId();
    const key = randomId();
    this.s.sessions[session] = {
      ch,
      at: t,
      mode: plan.mode,
      counted: 0,
      tokenExp: 0,
      key,
      ...(cred.kind === "pass" ? { pass: cred.id, inv: cred.inv } : { open: cred.id, ip }),
    };
    if (cred.kind === "pass") this.s.passes[cred.id]!.last = t;
    await this.save();
    return { ok: true, session, key, mode: plan.mode, channels: this.channelsFor(cred) };
  }

  /** minted records the expiry of the token just minted for a session. */
  async minted(session: string, expMs: number): Promise<void> {
    const x = this.s.sessions[session];
    if (!x) return;
    x.tokenExp = expMs;
    await this.save();
  }

  /**
   * beat keeps a session alive: it counts the credential's daily minutes, says whether the token
   * needs refreshing (past half its life), and ends a session whose invite or pass was revoked
   * or that went 150 s without a beat.
   */
  async beat(session: string, key: string, refreshAfterMs: number): Promise<BeatResult> {
    const t = now();
    const x = this.s.sessions[session];
    // a wrong key answers as an ended session, and changes nothing
    if (x && !sameText(x.key, key)) return { ok: false, status: 410, error: "session_ended" };
    if (!x || t - x.at > SESSION_TTL_MS) {
      if (x) delete this.s.sessions[session];
      this.prune(t);
      await this.save();
      return { ok: false, status: 410, error: "session_ended" };
    }
    if (this.revoked(x.inv, x.pass) || (x.pass && !this.s.passes[x.pass])) {
      delete this.s.sessions[session];
      await this.save();
      return { ok: false, status: 410, error: "revoked" };
    }
    x.at = t;
    if (t - x.counted >= MINUTE_GAP_MS) {
      x.counted = t;
      const id = x.pass ? `p:${x.pass}` : `o:${x.open}`;
      const today = day(t);
      const d = this.s.daily[id];
      this.s.daily[id] =
        d && d.day === today ? { day: today, minutes: d.minutes + 1 } : { day: today, minutes: 1 };
    }
    if (x.pass && this.s.passes[x.pass]) this.s.passes[x.pass]!.last = t;
    this.prune(t);
    await this.save();
    return {
      ok: true,
      ch: x.ch,
      mode: x.mode,
      refresh: x.tokenExp - t <= refreshAfterMs,
      concurrent: Object.keys(this.s.sessions).length,
    };
  }

  /**
   * spend records an attestation challenge as used until it expires (the challenge itself is
   * stateless, signed by the edge): true the first time. Full, it refuses rather than forget a
   * challenge still live.
   */
  async spend(nonce: string, expMs: number): Promise<boolean | "full"> {
    const t = now();
    const n = (this.s.nonces ??= {});
    if (n[nonce] !== undefined) return false;
    for (const [k, exp] of Object.entries(n)) if (exp <= t) delete n[k];
    if (Object.keys(n).length >= MAX_SPENT) return "full";
    n[nonce] = expMs;
    await this.save();
    return true;
  }

  /** passes is what the publisher sees: each device's random id and dates, and each invite's uses. */
  async passes(): Promise<{
    passes: { id: string; inv: string; first_use: number; last_use: number; revoked: boolean }[];
    uses: Record<string, number>;
  }> {
    const uses: Record<string, number> = {};
    const out = Object.values(this.s.passes).map((p) => {
      uses[p.inv] = (uses[p.inv] ?? 0) + 1;
      return {
        id: p.id,
        inv: p.inv,
        first_use: p.first,
        last_use: p.last,
        revoked: this.revoked(p.inv, p.id),
      };
    });
    return { passes: out, uses };
  }

  /** concurrent is the live sessions (tests and the publisher's page). */
  async concurrent(): Promise<{ total: number; byChannel: Record<string, number> }> {
    this.prune(now());
    const byChannel: Record<string, number> = {};
    for (const x of Object.values(this.s.sessions)) byChannel[x.ch] = (byChannel[x.ch] ?? 0) + 1;
    return { total: Object.keys(this.s.sessions).length, byChannel };
  }
}

/** validSettings checks an access post's payload shape. */
export function validSettings(p: Record<string, unknown>): AccessSettings | null {
  const strs = (v: unknown) => Array.isArray(v) && v.every((x) => typeof x === "string");
  const obj = (v: unknown) => typeof v === "object" && v !== null && !Array.isArray(v);
  if (
    typeof p.pub !== "string" ||
    !p.pub ||
    typeof p.version !== "number" ||
    !obj(p.channels) ||
    typeof p.open_allowed !== "boolean" ||
    typeof p.max_sessions_per_ip !== "number" ||
    typeof p.daily_minutes !== "number" ||
    !strs(p.revoked_invites) ||
    !strs(p.revoked_passes) ||
    !obj(p.codes)
  )
    return null;
  for (const c of Object.values(p.channels as Record<string, unknown>)) {
    const x = c as Record<string, unknown>;
    if (
      !obj(c) ||
      (x.access !== "invite" && x.access !== "open") ||
      typeof x.max_viewers !== "number"
    )
      return null;
  }
  return {
    pub: p.pub,
    version: p.version,
    channels: p.channels as Record<string, AccessChannel>,
    open_allowed: p.open_allowed,
    max_sessions_per_ip: p.max_sessions_per_ip,
    daily_minutes: p.daily_minutes,
    revoked_invites: p.revoked_invites as string[],
    revoked_passes: p.revoked_passes as string[],
    codes: p.codes as Record<string, string>,
  };
}

/**
 * handleAccessControl takes the cloud's viewer-access posts (on the ingest host, signed with a
 * CLOUD_PUBKEYS key like every control post):
 *   PUT  /v1/control/access   a publisher's access state; 409 when the edge holds a newer one
 *   POST /v1/control/passes   {pub}: the publisher's guest passes and each invite's uses
 */
export async function handleAccessControl(
  request: Request,
  env: Env,
  path: string
): Promise<Response> {
  const t = now();
  if (path === "/v1/control/access" && request.method === "PUT") {
    const v = await verifyControl(await request.text(), "access", env.CLOUD_PUBKEYS, t);
    if (!v.ok) return problem(v.error === "wrong_type" ? 400 : 401, v.error);
    const a = validSettings(v.payload);
    if (!a) return problem(400, "bad_access");
    const r = await env.VIEWER.get(env.VIEWER.idFromName(a.pub)).install(a);
    return r === "applied" ? json(200, { version: a.version }) : problem(409, "stale");
  }
  if (path === "/v1/control/passes" && request.method === "POST") {
    const v = await verifyControl(await request.text(), "passes", env.CLOUD_PUBKEYS, t);
    if (!v.ok) return problem(v.error === "wrong_type" ? 400 : 401, v.error);
    if (typeof v.payload.pub !== "string" || !v.payload.pub) return problem(400, "bad_request");
    return json(200, await env.VIEWER.get(env.VIEWER.idFromName(v.payload.pub)).passes());
  }
  return problem(404, "not_found");
}
