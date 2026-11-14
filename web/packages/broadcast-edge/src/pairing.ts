// TV pairing (story 50.27 §2): a TV app with no keyboard shows a QR code; a phone opens it and
// sends the TV a credential, with no account on either side.
//
//   POST /api/v1/pair            the TV: a new pairing {nonce, url, expires_at} (10 minutes)
//   GET  /api/v1/pair/<nonce>    the TV's long-poll: 200 {pass…} once, 204 not yet, 410 gone
//   POST /api/v1/pair/<nonce>    the phone: {invite} or {publisher, code} (a new device use of
//                                the invite), or {publisher, open: true} with its own open
//                                session (a fresh one for the TV)
//
// One Pairing Durable Object per nonce. The nonce is 128 random bits made by the edge, shown only
// in the QR code; the credential is handed over once and then forgotten, and everything is
// deleted when the 10 minutes end.
import { DurableObject } from "cloudflare:workers";
import { now } from "./clock";
import type { Env } from "./env";

/** A pairing lives 10 minutes (the spec's long-poll window). */
export const PAIR_TTL_MS = 10 * 60 * 1000;
/** One long-poll waits this long at most before answering 204. */
export const PAIR_WAIT_MS = 25_000;
export const NONCE_RE = /^[A-Za-z0-9_-]{22}$/;

/** What the TV receives: the same fields as a redeem answer (or an open session's). */
export interface Handover {
  publisher: string;
  /** a guest pass (mkbg1…) or an open session (mkbo1…), kept in the keychain or keystore */
  credential: string;
  kind: "pass" | "open";
  channels: string[];
  expires_at: string;
}

interface State {
  exp: number;
  handover?: Handover;
  taken?: boolean;
}

export type TakeResult = { status: "ready"; handover: Handover } | { status: "pending" | "gone" };

export class Pairing extends DurableObject<Env> {
  private waiters: ((h: Handover | null) => void)[] = [];

  private async state(): Promise<State | undefined> {
    return this.ctx.storage.get<State>("s");
  }

  /** open starts a pairing; a nonce already in use is refused (false). */
  async open(): Promise<boolean> {
    if (await this.state()) return false;
    const exp = now() + PAIR_TTL_MS;
    await this.ctx.storage.put("s", { exp } satisfies State);
    await this.ctx.storage.setAlarm(exp);
    return true;
  }

  /** live says whether the phone may still deliver to this pairing. */
  async live(): Promise<boolean> {
    const s = await this.state();
    return !!s && s.exp > now() && !s.handover && !s.taken;
  }

  /** deliver hands a credential to the waiting TV: false when the pairing is gone or used. */
  async deliver(h: Handover): Promise<boolean> {
    const s = await this.state();
    if (!s || s.exp <= now() || s.handover || s.taken) return false;
    s.handover = h;
    await this.ctx.storage.put("s", s);
    const ws = this.waiters;
    this.waiters = [];
    for (const w of ws) w(h);
    return true;
  }

  /** take answers the TV's poll: the credential once, waiting up to `waitMs` for it. */
  async take(waitMs: number): Promise<TakeResult> {
    const s = await this.state();
    if (!s || s.exp <= now() || s.taken) return { status: "gone" };
    if (!s.handover && waitMs > 0) {
      const h = await new Promise<Handover | null>((resolve) => {
        const timer = setTimeout(() => {
          this.waiters = this.waiters.filter((w) => w !== done);
          resolve(null);
        }, waitMs);
        const done = (x: Handover | null) => {
          clearTimeout(timer);
          resolve(x);
        };
        this.waiters.push(done);
      });
      if (!h) return { status: "pending" };
    }
    const cur = await this.state();
    if (!cur?.handover || cur.taken) return cur?.taken ? { status: "gone" } : { status: "pending" };
    const handover = cur.handover;
    // handed over once: the credential is no longer kept here
    await this.ctx.storage.put("s", { exp: cur.exp, taken: true } satisfies State);
    return { status: "ready", handover };
  }

  override async alarm(): Promise<void> {
    for (const w of this.waiters) w(null);
    this.waiters = [];
    await this.ctx.storage.deleteAll();
  }
}
