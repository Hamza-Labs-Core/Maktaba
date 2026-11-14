// TV pairing (story 50.27 §2): a TV app with no keyboard shows a QR code; a phone opens it and
// sends the TV a credential, with no account on either side.
//
//   POST /api/v1/pair            the TV: a new pairing {nonce, poll_key, code, url, expires_at}
//   GET  /api/v1/pair/<nonce>    the TV's long-poll, with X-Maktaba-Pair-Key: <poll_key>:
//                                200 {credential…} once, 204 not yet, 410 gone
//   POST /api/v1/pair/<nonce>    the phone: {invite} or {publisher, code} (a new device use of
//                                the invite), or {publisher, open: true}: a token of the phone's
//                                open session (the same id and expiry, so the same limits)
//
// One Pairing Durable Object per nonce. The nonce is 128 random bits made by the edge and shown
// in the QR code. Only the TV holds the poll key (never in the QR code or a URL), so someone who
// sees the QR code cannot collect what the phone sends. The TV and the phone's page both show a
// 4-character code, so the guest can check the page belongs to the TV in front of them (device
// flow, RFC 8628 §5.4). The credential is handed over once and then forgotten, and everything is
// deleted when the 10 minutes end.
import { DurableObject } from "cloudflare:workers";
import { now } from "./clock";
import { sameText } from "./crypto";
import type { Env } from "./env";

/** A pairing lives 10 minutes (the spec's long-poll window). */
export const PAIR_TTL_MS = 10 * 60 * 1000;
/** One long-poll waits this long at most before answering 204. */
export const PAIR_WAIT_MS = 25_000;
export const NONCE_RE = /^[A-Za-z0-9_-]{22}$/;
export const PAIR_KEY_HEADER = "X-Maktaba-Pair-Key";
/** The code's letters: no 0/O or 1/I, which a TV's font makes alike. */
const CODE_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";

/** pairCode is the 4-character code both screens show. */
export function pairCode(): string {
  const b = crypto.getRandomValues(new Uint8Array(4));
  return Array.from(b, (x) => CODE_ALPHABET[x % CODE_ALPHABET.length]).join("");
}

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
  /** the TV's secret for its polls */
  key: string;
  /** the code both screens show */
  code: string;
  handover?: Handover;
  taken?: boolean;
}

export type TakeResult = { status: "ready"; handover: Handover } | { status: "pending" | "gone" };

export class Pairing extends DurableObject<Env> {
  private waiters: ((h: Handover | null) => void)[] = [];

  private async state(): Promise<State | undefined> {
    return this.ctx.storage.get<State>("s");
  }

  /** open starts a pairing with the TV's poll key and the code to show; a nonce in use: false. */
  async open(key: string, code: string): Promise<boolean> {
    if (await this.state()) return false;
    const exp = now() + PAIR_TTL_MS;
    await this.ctx.storage.put("s", { exp, key, code } satisfies State);
    await this.ctx.storage.setAlarm(exp);
    return true;
  }

  /** live is the code to show while the phone may still deliver to this pairing, else null. */
  async live(): Promise<string | null> {
    const s = await this.state();
    return !!s && s.exp > now() && !s.handover && !s.taken ? s.code : null;
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

  /** take answers the TV's poll (with its key): the credential once, waiting up to `waitMs`. */
  async take(key: string, waitMs: number): Promise<TakeResult> {
    const s = await this.state();
    if (!s || s.exp <= now() || s.taken || !sameText(s.key, key)) return { status: "gone" };
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
    await this.ctx.storage.put("s", {
      exp: cur.exp,
      key: cur.key,
      code: cur.code,
      taken: true,
    } satisfies State);
    return { status: "ready", handover };
  }

  override async alarm(): Promise<void> {
    for (const w of this.waiters) w(null);
    this.waiters = [];
    await this.ctx.storage.deleteAll();
  }
}
