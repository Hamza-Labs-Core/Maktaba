// Revocations (story 50.21 §1, filled by 50.25): one Durable Object per environment holding the
// cloud-signed broadcast revocation list, the kill switch's word at the edge.
//
// The list is a control post (tokens.ts) of typ "revocations", signed with the broadcast signing
// key (cloud/internal/broadcast/revocations.go):
//   {typ, kid, iat, exp, schema: 1, version, issued_at, expires_at, entries: [Entry, ...]}
//
//   action      viewers                                          ingest
//   slate       the channel's playlists show its slate until     accepted
//               `until` (a programme entry)
//   off_air     503 off_air on play and at the next playlist     403 revoked
//   suspend     off_air for every channel of the home/publisher  403 revoked
//   terminate   as suspend (the cloud refuses a new publish)     403 revoked
//
// The version only goes up: an older list is refused (409), an equal one is a no-op. A list
// with a bad signature or an unknown key is refused and logged as an alert (SEV2). Installing a
// newer list hands it to every channel it names and every channel of a publisher it names; each
// ChannelLive also re-reads the list at most every 10 s, which catches a home or global entry
// and a missed fan-out. The break-glass variables (BROADCAST_GLOBAL_OFF, BROADCAST_OFF_CHANNELS)
// are checked before any of this, on every request. The cloud pushes each change; the Worker's
// cron pulls GET /v1/broadcast/revocations every minute as a backup.
import { DurableObject } from "cloudflare:workers";
import { now } from "./clock";
import { signEdgeRequest } from "./edge_sign";
import { channelOff, globallyOff, type Env } from "./env";
import { log } from "./log";
import { verifyControl } from "./tokens";

export type Kind = "channel" | "programme" | "publisher" | "home" | "global";
export type Action = "slate" | "off_air" | "suspend" | "terminate";

export interface Entry {
  id: string;
  kind: Kind;
  ref?: string;
  action: Action;
  reason: string;
  since?: string;
  until?: string;
}

export interface RevocationList {
  version: number;
  entries: Entry[];
}

export const EMPTY_LIST: RevocationList = { version: 0, entries: [] };

/** Who a request is about: the channel always, its publisher and home when known. */
export interface Who {
  ch: string;
  pub?: string | null;
  home?: string | null;
}

/**
 * What the list (or break-glass) says of a channel now: `off_air` stops it (viewers and
 * ingest), `slate` shows the slate to viewers until `until`; null is nothing.
 */
export type Verdict =
  | { stop: "off_air"; action: Exclude<Action, "slate">; reason: string; id: string }
  | { stop: "slate"; action: "slate"; reason: string; id: string; until: number };

export type InstallResult =
  | { ok: true; version: number; channels: number }
  | { ok: false; status: 400 | 401 | 409; error: string };

const KINDS: readonly string[] = ["channel", "programme", "publisher", "home", "global"];
const ACTIONS: readonly string[] = ["slate", "off_air", "suspend", "terminate"];

/** parseEntries reads a list's entries strictly: one bad entry refuses the list. */
export function parseEntries(v: unknown): Entry[] | null {
  if (!Array.isArray(v)) return null;
  const out: Entry[] = [];
  for (const x of v) {
    if (typeof x !== "object" || x === null) return null;
    const e = x as Record<string, unknown>;
    if (
      typeof e.id !== "string" ||
      typeof e.kind !== "string" ||
      !KINDS.includes(e.kind) ||
      typeof e.action !== "string" ||
      !ACTIONS.includes(e.action) ||
      typeof e.reason !== "string" ||
      (e.ref !== undefined && typeof e.ref !== "string") ||
      (e.until !== undefined && (typeof e.until !== "string" || isNaN(Date.parse(e.until))))
    )
      return null;
    if (e.kind !== "global" && !e.ref) return null;
    if ((e.kind === "programme") !== (e.action === "slate") || (e.action === "slate" && !e.until))
      return null;
    out.push({
      id: e.id,
      kind: e.kind as Kind,
      ref: e.ref as string | undefined,
      action: e.action as Action,
      reason: e.reason,
      since: typeof e.since === "string" ? e.since : undefined,
      until: e.until as string | undefined,
    });
  }
  return out;
}

function applies(e: Entry, who: Who): boolean {
  switch (e.kind) {
    case "global":
      return true;
    case "channel":
    case "programme":
      return e.ref === who.ch;
    case "publisher":
      return !!who.pub && e.ref === who.pub;
    case "home":
      return !!who.home && e.ref === who.home;
  }
}

/** verdictFor is what the list says of a channel at t: an off_air beats a slate. */
export function verdictFor(list: RevocationList, who: Who, t: number): Verdict | null {
  let slate: Verdict | null = null;
  for (const e of list.entries) {
    if (!applies(e, who)) continue;
    if (e.action === "slate") {
      const until = Date.parse(e.until ?? "");
      if (until > t && (!slate || (slate.stop === "slate" && until > slate.until)))
        slate = { stop: "slate", action: "slate", reason: e.reason, id: e.id, until };
      continue;
    }
    return { stop: "off_air", action: e.action, reason: e.reason, id: e.id };
  }
  return slate;
}

/** breakGlass is the owner's Worker variables, checked first on every request (§3). */
export function breakGlass(env: Env, ch: string): Verdict | null {
  if (globallyOff(env) || channelOff(env, ch))
    return { stop: "off_air", action: "off_air", reason: "incident", id: "break-glass" };
  return null;
}

/** sameVerdict compares two verdicts by what a viewer or the home would notice. */
export function sameVerdict(a: Verdict | null, b: Verdict | null): boolean {
  if (!a || !b) return a === b;
  return (
    a.stop === b.stop &&
    a.action === b.action &&
    a.reason === b.reason &&
    (a.stop !== "slate" || (b.stop === "slate" && a.until === b.until))
  );
}

export class Revocations extends DurableObject<Env> {
  private list: RevocationList = EMPTY_LIST;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    ctx.blockConcurrencyWhile(async () => {
      const saved = await ctx.storage.get<RevocationList>("list");
      // a list saved before entries existed (50.21's empty one) reads as empty
      this.list = saved && Array.isArray(saved.entries) ? saved : EMPTY_LIST;
    });
  }

  async current(): Promise<RevocationList> {
    return this.list;
  }

  /** install verifies a signed list and applies it if it is newer than the one held. */
  async install(text: string): Promise<InstallResult> {
    const v = await verifyControl(text, "revocations", this.env.CLOUD_PUBKEYS, now());
    if (!v.ok) {
      if (v.error === "bad_signature" || v.error === "unknown_key")
        log("error", "revocation list refused: bad signature", {
          component: "revocations",
          attrs: { alert: "SEV2", event: "broadcast_revocations_bad_signature", error: v.error },
        });
      return {
        ok: false,
        status: v.error === "wrong_type" || v.error === "bad_token" ? 400 : 401,
        error: v.error,
      };
    }
    const p = v.payload;
    const entries = parseEntries(p.entries);
    if (typeof p.version !== "number" || p.schema !== 1 || !entries)
      return { ok: false, status: 400, error: "bad_list" };
    if (p.version < this.list.version) return { ok: false, status: 409, error: "older_list" };
    if (p.version === this.list.version) return { ok: true, version: p.version, channels: 0 };

    const old = this.list;
    const next: RevocationList = { version: p.version, entries };
    this.list = next;
    await this.ctx.storage.put("list", next);
    // every channel either list names, directly or through its publisher: each re-reads its
    // verdict from the new list (a channel no longer named is restored)
    const named = async (l: RevocationList) => {
      const out: string[] = [];
      for (const e of l.entries) {
        if ((e.kind === "channel" || e.kind === "programme") && e.ref) out.push(e.ref);
        if (e.kind === "publisher" && e.ref)
          out.push(
            ...(await this.env.PUBLISHER.get(this.env.PUBLISHER.idFromName(e.ref)).channels())
          );
      }
      return out;
    };
    const chans = new Set([...(await named(old)), ...(await named(next))]);
    await Promise.all(
      [...chans].map((c) =>
        this.env.CHANNEL.get(this.env.CHANNEL.idFromName(c))
          .applyList(next, c)
          .catch((err: unknown) =>
            // the channel catches up at its next 10 s check
            log("warn", "revocation fan-out missed a channel", {
              component: "revocations",
              channel: c,
              err,
            })
          )
      )
    );
    log("info", "revocation list installed", {
      component: "revocations",
      attrs: { version: next.version, entries: next.entries.length, channels: chans.size },
    });
    return { ok: true, version: next.version, channels: chans.size };
  }
}

let cached: { at: number; list: RevocationList } | null = null;
const CACHE_MS = 10_000;

/** revocationsFor is the list as this isolate last read it (at most 10 s old). */
export async function revocationsFor(env: Env): Promise<RevocationList> {
  const t = now();
  if (cached && t - cached.at < CACHE_MS) return cached.list;
  const list = await env.REVOCATIONS.get(env.REVOCATIONS.idFromName("global")).current();
  cached = { at: t, list };
  return list;
}

/** forgetRevocations drops the isolate's copy (after an install, and in tests). */
export function forgetRevocations(): void {
  cached = null;
}

/** stopVerdict is break-glass, then the list, for a request about `who` (ingest, beats). */
export async function stopVerdict(env: Env, who: Who, t: number): Promise<Verdict | null> {
  return breakGlass(env, who.ch) ?? verdictFor(await revocationsFor(env), who, t);
}

export const REVOCATIONS_PATH = "/v1/broadcast/revocations";

/**
 * pullRevocations is the backup to the cloud's push (§2): the Worker's cron asks the API for
 * the signed list every minute, with the edge's signature, and installs it. Without API_ORIGIN
 * or EDGE_SIGNING_KEY there is nothing to ask.
 */
export async function pullRevocations(
  env: Env,
  fetcher: typeof fetch = fetch
): Promise<InstallResult | null> {
  if (!env.API_ORIGIN || !env.EDGE_SIGNING_KEY) return null;
  const t = now();
  const headers = await signEdgeRequest(
    env.EDGE_SIGNING_KEY,
    "GET",
    REVOCATIONS_PATH,
    t,
    new Uint8Array()
  );
  const res = await fetcher(new URL(REVOCATIONS_PATH, env.API_ORIGIN).toString(), { headers });
  if (!res.ok) {
    log("warn", "revocation pull failed", {
      component: "revocations",
      attrs: { status: res.status },
    });
    return null;
  }
  const r = await env.REVOCATIONS.get(env.REVOCATIONS.idFromName("global")).install(
    await res.text()
  );
  forgetRevocations();
  return r;
}
