// AccountGuard (story 54.15 §4): one per sign-in name (a lower-cased email, whether or not
// an account has it, so a lockout never tells), counting failed sign-ins. Five failures
// lock it for 15 minutes; each later lock doubles, up to 24 hours; a day with no failure
// starts again from 15 minutes.
import { DurableObject } from "cloudflare:workers";

const FAILS = 5;
const FIRST = 15 * 60_000;
const MAX = 24 * 3600_000;

interface State {
  fails: number;
  level: number;
  lockedUntil: number;
  lastFail: number;
}

export class AccountGuard extends DurableObject {
  private async state(): Promise<State> {
    return (await this.ctx.storage.get<State>("s")) ?? { fails: 0, level: 0, lockedUntil: 0, lastFail: 0 };
  }

  /** Whether a sign-in may be tried now, and until when it is locked. */
  async check(at: number): Promise<{ locked: boolean; until: number }> {
    const s = await this.state();
    return { locked: s.lockedUntil > at, until: s.lockedUntil };
  }

  /** Records a failure; answers whether this one locked it (the account's owner is told). */
  async fail(at: number): Promise<{ locked: boolean; newlyLocked: boolean; until: number }> {
    const s = await this.state();
    if (at - s.lastFail > MAX) s.level = 0;
    s.lastFail = at;
    if (s.lockedUntil > at) return { locked: true, newlyLocked: false, until: s.lockedUntil };
    s.fails += 1;
    let newlyLocked = false;
    if (s.fails >= FAILS) {
      s.lockedUntil = at + Math.min(FIRST * 2 ** s.level, MAX);
      s.level += 1;
      s.fails = 0;
      newlyLocked = true;
    }
    await this.ctx.storage.put("s", s);
    return { locked: newlyLocked, newlyLocked, until: s.lockedUntil };
  }

  async succeed(): Promise<void> {
    const s = await this.state();
    s.fails = 0;
    await this.ctx.storage.put("s", s);
  }
}
