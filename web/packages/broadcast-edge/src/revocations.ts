// Revocations (story 50.21 §1, filled by 50.25): one Durable Object per environment holding the
// cloud-signed revocation list. A list names publishers, channels and homes the edge must refuse.
// Installing a newer list revokes the channels it adds (each ChannelLive answers 403 revoked at
// its next playlist and tells its home over the control socket) and restores the ones it drops.
//
// The list is a control post (tokens.ts), typ "revocations":
//   {typ, kid, iat, exp, version, pubs: [...], channels: [...], homes: [...]}
// The Worker reads the list into each isolate for 10 s (revocationsFor), so the ingest path
// adds no Durable Object call per segment; the channels themselves hold the revoked flag.
import { DurableObject } from "cloudflare:workers";
import { now } from "./clock";
import type { Env } from "./env";
import { verifyControl } from "./tokens";

export interface RevocationList {
  version: number;
  pubs: string[];
  channels: string[];
  homes: string[];
}

export const EMPTY_LIST: RevocationList = { version: 0, pubs: [], channels: [], homes: [] };

export type InstallResult =
  | { ok: true; version: number; revoked: number; restored: number }
  | { ok: false; status: 400 | 401 | 409; error: string };

function strings(v: unknown): string[] | null {
  return Array.isArray(v) && v.every((x) => typeof x === "string") ? (v as string[]) : null;
}

export class Revocations extends DurableObject<Env> {
  private list: RevocationList = EMPTY_LIST;

  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    ctx.blockConcurrencyWhile(async () => {
      this.list = (await ctx.storage.get<RevocationList>("list")) ?? EMPTY_LIST;
    });
  }

  async current(): Promise<RevocationList> {
    return this.list;
  }

  /** install verifies a signed list and applies it if it is newer than the one held. */
  async install(text: string): Promise<InstallResult> {
    const v = await verifyControl(text, "revocations", this.env.CLOUD_PUBKEYS, now());
    if (!v.ok)
      return {
        ok: false,
        status: v.error === "wrong_type" || v.error === "bad_token" ? 400 : 401,
        error: v.error,
      };
    const p = v.payload;
    const pubs = strings(p.pubs ?? []);
    const channels = strings(p.channels ?? []);
    const homes = strings(p.homes ?? []);
    if (typeof p.version !== "number" || !pubs || !channels || !homes)
      return { ok: false, status: 400, error: "bad_list" };
    if (p.version < this.list.version) return { ok: false, status: 409, error: "older_list" };
    if (p.version === this.list.version)
      return { ok: true, version: p.version, revoked: 0, restored: 0 };

    const old = this.list;
    const next: RevocationList = { version: p.version, pubs, channels, homes };
    const chOf = async (pubList: string[]) =>
      (
        await Promise.all(
          pubList.map((pub) =>
            this.env.PUBLISHER.get(this.env.PUBLISHER.idFromName(pub)).channels()
          )
        )
      ).flat();
    const before = new Set([...old.channels, ...(await chOf(old.pubs))]);
    const after = new Set([...next.channels, ...(await chOf(next.pubs))]);
    this.list = next;
    await this.ctx.storage.put("list", next);
    const stub = (ch: string) => this.env.CHANNEL.get(this.env.CHANNEL.idFromName(ch));
    // every listed channel is (re)told: revoke is idempotent, and a channel published since the
    // last list must hear it too
    const add = [...after];
    const drop = [...before].filter((c) => !after.has(c));
    await Promise.all([
      ...add.map((c) => stub(c).revoke("revoked")),
      ...drop.map((c) => stub(c).restore()),
    ]);
    return {
      ok: true,
      version: next.version,
      revoked: [...after].filter((c) => !before.has(c)).length,
      restored: drop.length,
    };
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

export function isRevoked(
  list: RevocationList,
  who: { pub: string; ch: string; home?: string }
): boolean {
  return (
    list.pubs.includes(who.pub) ||
    list.channels.includes(who.ch) ||
    (!!who.home && list.homes.includes(who.home))
  );
}
