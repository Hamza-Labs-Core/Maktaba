// IpBucket (story 54.15 §4, §5): one per client address or IPv6 /64 (rate limits on sign-up,
// sign-in, email sends and pairing-code entry), and one per linked home ("home:<id>") whose
// nonce cache refuses a replayed signed request.
import { DurableObject } from "cloudflare:workers";

export class IpBucket extends DurableObject {
  /** Takes one from action's budget of limit per window; false when it is spent. */
  async take(action: string, limit: number, windowMs: number, at: number): Promise<boolean> {
    const key = `rate:${action}`;
    const w = (await this.ctx.storage.get<{ start: number; n: number }>(key)) ?? { start: at, n: 0 };
    if (at - w.start >= windowMs) {
      w.start = at;
      w.n = 0;
    }
    if (w.n >= limit) return false;
    w.n += 1;
    await this.ctx.storage.put(key, w);
    return true;
  }

  /** Records a nonce for ttl; false when it was seen (a replay). */
  async nonce(n: string, ttlMs: number, at: number): Promise<boolean> {
    const key = `nonce:${n}`;
    const seen = await this.ctx.storage.get<number>(key);
    if (seen !== undefined && seen > at) return false;
    await this.ctx.storage.put(key, at + ttlMs);
    const alarm = await this.ctx.storage.getAlarm();
    if (alarm === null) await this.ctx.storage.setAlarm(at + ttlMs);
    return true;
  }

  async alarm(): Promise<void> {
    const at = Date.now();
    const all = await this.ctx.storage.list<number>({ prefix: "nonce:" });
    const old: string[] = [];
    let next = 0;
    for (const [k, until] of all) {
      if (until <= at) old.push(k);
      else next = next === 0 ? until : Math.min(next, until);
    }
    if (old.length) await this.ctx.storage.delete(old);
    if (next) await this.ctx.storage.setAlarm(next);
  }
}
