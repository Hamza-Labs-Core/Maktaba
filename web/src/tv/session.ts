// Epic 58 story 58.09 — /tv/'s sign-in inside the Samsung or LG launcher.
//
// The launcher's page is a packaged file:// page, and /tv/ is its iframe: the browser treats
// every request from that frame as cross-site and never sends the home's SameSite=Lax
// session cookie (checked in Chromium: a cookie set first-party is missing from the same
// fetch made in such a frame). So in the launcher /tv/ signs in by code, the home's own
// pairing code (POST /api/pairing/device, then its token poll), and keeps the tokens the
// home hands back in the home's own origin (localStorage here), never in the launcher's and
// never through postMessage. Every API call then carries `Authorization: Bearer` (lib/api.ts),
// and an access token that runs out is renewed with POST /api/auth/refresh.
//
// A TV is a device signed in as the person who approved it: the home's own rules decide
// its role, libraries and parental policy, as for any device approved there.

import { CLIENT_HEADER, clientHeaderValue } from "../lib/clientHeader";
import { apiUrl, type BearerAuth } from "../lib/api";

const KEY = "mkt:tv:session";

/** The token answer of POST /api/pairing/device/token and POST /api/auth/refresh. */
export interface TokenAnswer {
  access_token: string;
  access_expires_in: number;
  refresh_token: string;
  refresh_expires_in?: number;
}

interface Stored {
  access: string;
  /** When the access token runs out (ms since the epoch). */
  accessUntil: number;
  refresh: string;
}

function read(): Stored | null {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(KEY) || "null");
    if (!raw || typeof raw !== "object") return null;
    const s = raw as Partial<Stored>;
    if (typeof s.access !== "string" || typeof s.refresh !== "string" || !s.refresh) return null;
    return { access: s.access, accessUntil: Number(s.accessUntil) || 0, refresh: s.refresh };
  } catch {
    return null;
  }
}

function write(s: Stored | null): void {
  try {
    if (s) localStorage.setItem(KEY, JSON.stringify(s));
    else localStorage.removeItem(KEY);
  } catch {
    // storage refused: the session holds for this run only
  }
}

export function isTokenAnswer(v: unknown): v is TokenAnswer {
  if (!v || typeof v !== "object") return false;
  const t = v as Partial<TokenAnswer>;
  return typeof t.access_token === "string" && typeof t.refresh_token === "string";
}

/** The TV's bearer session (lib/api.ts's BearerAuth). */
export class TvSession implements BearerAuth {
  private s: Stored | null = read();
  private renewing: Promise<boolean> | null = null;

  constructor(private readonly now: () => number = () => Date.now()) {}

  signedIn(): boolean {
    return !!this.s;
  }

  token(): string | null {
    return this.s?.access ?? null;
  }

  /** Keeps the tokens a code sign-in (or a refresh) handed back. */
  save(t: TokenAnswer): void {
    this.s = {
      access: t.access_token,
      accessUntil: this.now() + Math.max(0, t.access_expires_in || 0) * 1000,
      refresh: t.refresh_token,
    };
    write(this.s);
  }

  clear(): void {
    this.s = null;
    write(null);
  }

  logoutBody(): { refresh_token: string } | undefined {
    return this.s ? { refresh_token: this.s.refresh } : undefined;
  }

  /** A new access token (one renewal at a time); false, and signed out, when refused. */
  refresh(): Promise<boolean> {
    if (!this.s) return Promise.resolve(false);
    if (!this.renewing) {
      const refreshToken = this.s.refresh;
      this.renewing = this.renew(refreshToken).finally(() => {
        this.renewing = null;
      });
    }
    return this.renewing;
  }

  private async renew(refreshToken: string): Promise<boolean> {
    let res: Response;
    try {
      res = await fetch(apiUrl("/api/auth/refresh"), {
        method: "POST",
        credentials: "omit",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
          [CLIENT_HEADER]: clientHeaderValue(),
        },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
    } catch {
      // no answer: keep the tokens, the next call tries again
      return false;
    }
    if (res.ok) {
      const body: unknown = await res.json().catch(() => null);
      if (isTokenAnswer(body)) {
        this.save(body);
        return true;
      }
    }
    // refused (expired, revoked, replayed, a build below the home's floor): signed out
    if (res.status >= 400 && res.status < 500 && res.status !== 429) this.clear();
    return false;
  }
}
