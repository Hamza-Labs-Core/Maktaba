// PairingCode (story 54.15 §5): one per active link code, named by the code's first four
// characters. The home holds the device code (whose hash this keeps) and shows the user
// code; the admin types it on /link. A wrong second half counts against this code: the
// fifth wrong entry kills it (410 code_expired), so the home must start again.
import { DurableObject } from "cloudflare:workers";

export interface Pending {
  rest: string;
  deviceHash: string;
  publicKey: string;
  name: string;
  serverVersion: string;
  platform: string;
  expires: number;
}

interface State extends Pending {
  wrong: number;
  dead: boolean;
  account?: string;
  homeId?: string;
}

export const MAX_WRONG = 5;

export class PairingCode extends DurableObject {
  /** Starts this code; false when the name is taken by a live code (pick another). */
  async create(p: Pending, at: number): Promise<boolean> {
    const s = await this.ctx.storage.get<State>("s");
    if (s && !s.dead && s.expires > at && !s.homeId) return false;
    await this.ctx.storage.put("s", { ...p, wrong: 0, dead: false });
    await this.ctx.storage.setAlarm(p.expires + 60_000);
    return true;
  }

  /** The home this code describes, when rest is its second half (and it is alive). */
  async confirm(
    rest: string,
    at: number,
  ): Promise<{ ok: true; home: Pending } | { ok: false; code: "code_expired" | "code_wrong"; left?: number }> {
    const s = await this.ctx.storage.get<State>("s");
    if (!s || s.dead || s.expires <= at || s.account) return { ok: false, code: "code_expired" };
    if (s.rest !== rest) {
      s.wrong += 1;
      if (s.wrong >= MAX_WRONG) s.dead = true;
      await this.ctx.storage.put("s", s);
      return s.dead ? { ok: false, code: "code_expired" } : { ok: false, code: "code_wrong", left: MAX_WRONG - s.wrong };
    }
    return { ok: true, home: s };
  }

  /** The admin confirmed: the store recorded the home as homeId. */
  async complete(account: string, homeId: string): Promise<void> {
    const s = await this.ctx.storage.get<State>("s");
    if (!s) return;
    s.account = account;
    s.homeId = homeId;
    await this.ctx.storage.put("s", s);
  }

  /** The home's poll: pending, its home id, or gone. */
  async poll(deviceHash: string, at: number): Promise<{ status: "pending" | "linked" | "expired"; homeId?: string }> {
    const s = await this.ctx.storage.get<State>("s");
    if (!s || s.deviceHash !== deviceHash) return { status: "expired" };
    if (s.homeId) return { status: "linked", homeId: s.homeId };
    if (s.dead || s.expires <= at) return { status: "expired" };
    return { status: "pending" };
  }

  async alarm(): Promise<void> {
    await this.ctx.storage.deleteAll();
  }
}
