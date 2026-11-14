// Story 58.09's second part — the cloud code (story 58.03) on the Samsung and LG launcher.
//
// A TV that is not on the home's network has no other way in, so the cloud code is the
// launcher's first screen. The launcher asks Maktaba Cloud for a code (POST /v1/tv/codes,
// no sign-in), shows it with a QR of its approve page (maktaba.tv/tv?c=…), and polls
// (POST /v1/tv/codes/poll {code, poll_secret}) until a person signed in to a home approves
// it: in the Player (Profile → Approve a TV) or on maktaba.tv/tv. The answer names the home,
// its connections (story 50.13's order: LAN first, the relay last) and a grant, the home's
// own single-use pairing secret. The launcher reaches the home by the first connection that
// answers as that home and hands the grant to the home's /tv/ over the bridge
// (`maktaba:signIn`), which redeems it in the home's origin (POST /api/pairing/device/token):
// the launcher never holds a token. A cloud code's TV is restricted on the home (never an
// admin, whoever approved it).
//
// The cloud answers these two routes to a packaged TV app's origin (`file://`, `null`) and
// to nothing else (cloud/internal/middleware/tvcors.go). The poll secret and the grant stay
// in memory: never stored, never in a URL, never in the log (log.ts redacts both fields).

import type { FetchLike } from "./discovery";
import { log } from "./log";

declare const __CLOUD_BASE__: string | undefined;

/** Maktaba Cloud's API (the build can point a test build elsewhere). */
export const CLOUD_BASE: string =
  typeof __CLOUD_BASE__ === "string" && __CLOUD_BASE__ ? __CLOUD_BASE__ : "https://api.maktaba.tv";

/** How long a cloud call gets before it counts as no answer. */
export const CLOUD_TIMEOUT_MS = 10000;

/** POST /v1/tv/codes's answer. */
export interface CloudCode {
  /** As shown: ABCD-2345. */
  code: string;
  poll_secret: string;
  expires_in: number;
  interval: number;
  /** The approve page the QR opens (https://maktaba.tv/tv?c=ABCD2345). */
  verify_url: string;
}

/** One of the home's connections, as the cloud lists them (story 50.13). */
export interface CloudConnection {
  url: string;
  kind: string;
  /** The certificate's name for a raw address (`local_ip`): a web runtime cannot pin it. */
  tls_name?: string;
}

/** What an approved code's poll hands the TV, once. */
export interface CloudApproval {
  home_id: string;
  home_name: string;
  connections: CloudConnection[];
  /** The home's single-use pairing secret: redeemed by /tv/, never logged or stored. */
  grant: string;
}

export type PollResult =
  | { kind: "pending" }
  | { kind: "slow_down" }
  | { kind: "gone" }
  | { kind: "approved"; approval: CloudApproval }
  | { kind: "error" };

/** The grant's shape, as the cloud checks it at claim (tvcodes.grantShape). */
export const GRANT_SHAPE = /^[A-Za-z0-9_-]{16,128}$/;

async function post(
  fetchFn: FetchLike,
  path: string,
  body: unknown,
  timeoutMs: number
): Promise<{ status: number; data: unknown } | null> {
  const ctl = typeof AbortController !== "undefined" ? new AbortController() : null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  try {
    const res = await Promise.race([
      fetchFn(`${CLOUD_BASE}${path}`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(body),
        ...(ctl ? { signal: ctl.signal } : {}),
      }),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => {
          ctl?.abort();
          reject(new Error("timeout"));
        }, timeoutMs);
      }),
    ]);
    const data: unknown = await res.json().catch(() => null);
    return { status: res.status, data };
  } catch {
    return null;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

function isCode(v: unknown): v is CloudCode {
  if (!v || typeof v !== "object") return false;
  const c = v as Partial<CloudCode>;
  return (
    typeof c.code === "string" &&
    c.code !== "" &&
    typeof c.poll_secret === "string" &&
    c.poll_secret !== "" &&
    typeof c.verify_url === "string"
  );
}

function isApproval(v: unknown): v is CloudApproval & { status: string } {
  if (!v || typeof v !== "object") return false;
  const a = v as Partial<CloudApproval> & { status?: unknown };
  return (
    a.status === "approved" &&
    typeof a.home_id === "string" &&
    a.home_id !== "" &&
    Array.isArray(a.connections) &&
    typeof a.grant === "string" &&
    GRANT_SHAPE.test(a.grant)
  );
}

/** A new code for this TV, or null when the cloud gave none (down, refused, too many). */
export async function createCode(
  fetchFn: FetchLike,
  platform: string,
  appVersion: string,
  timeoutMs = CLOUD_TIMEOUT_MS
): Promise<CloudCode | null> {
  const r = await post(
    fetchFn,
    "/v1/tv/codes",
    { platform, app_version: appVersion },
    timeoutMs
  );
  if (!r || r.status !== 201 || !isCode(r.data)) {
    log("launcher", `cloud code: none given (${r ? r.status : "no answer"})`);
    return null;
  }
  return r.data;
}

/** One poll of an open code. */
export async function pollCode(
  fetchFn: FetchLike,
  code: CloudCode,
  timeoutMs = CLOUD_TIMEOUT_MS
): Promise<PollResult> {
  const r = await post(
    fetchFn,
    "/v1/tv/codes/poll",
    { code: code.code, poll_secret: code.poll_secret },
    timeoutMs
  );
  if (!r) return { kind: "error" };
  if (r.status === 202) return { kind: "pending" };
  if (r.status === 429) return { kind: "slow_down" };
  if (r.status === 410 || r.status === 404) return { kind: "gone" };
  if (r.status === 200 && isApproval(r.data)) {
    const a = r.data;
    return {
      kind: "approved",
      approval: {
        home_id: a.home_id,
        home_name: typeof a.home_name === "string" ? a.home_name : "",
        connections: a.connections,
        grant: a.grant,
      },
    };
  }
  return { kind: "error" };
}

/**
 * The connections a web TV can use, in the cloud's order. A raw address pinned to a
 * certificate name (`local_ip` with its `tls_name`) is left out: a web runtime cannot check a
 * certificate against another name, so it would fail (or, worse, need a warning skipped).
 */
export function usableConnections(conns: unknown): string[] {
  if (!Array.isArray(conns)) return [];
  const out: string[] = [];
  for (const c of conns) {
    if (!c || typeof c !== "object") continue;
    const { url, tls_name } = c as Partial<CloudConnection>;
    if (typeof url !== "string") continue;
    let u: URL;
    try {
      u = new URL(url);
    } catch {
      continue;
    }
    if (u.protocol !== "https:" && u.protocol !== "http:") continue;
    if (u.username || u.password) continue;
    if (
      u.protocol === "https:" &&
      typeof tls_name === "string" &&
      tls_name !== "" &&
      tls_name.toLowerCase() !== u.hostname.toLowerCase()
    )
      continue;
    const base = `${u.protocol}//${u.host}${u.pathname.replace(/\/+$/, "")}`;
    if (out.indexOf(base) < 0) out.push(base);
  }
  return out;
}

/** What the code screen shows. */
export type CodeState =
  | { phase: "asking" }
  | { phase: "showing"; code: CloudCode; expiresAt: number }
  | { phase: "down" };

export interface CodeFlowDeps {
  fetch: FetchLike;
  platform: string;
  appVersion: string;
  onState(s: CodeState): void;
  onApproved(a: CloudApproval): void;
  /** setTimeout and clearTimeout (tests pass fakes). */
  after?: (fn: () => void, ms: number) => unknown;
  cancel?: (h: unknown) => void;
  now?: () => number;
}

/** How long the launcher waits before asking again when the cloud gave no code. */
export const RETRY_DOWN_MS = 30000;

/**
 * One run of the code screen: asks for a code, polls it at the cloud's interval (slower on
 * slow_down), asks for a new one when it runs out, and stops at the approval or stop().
 */
export class CodeFlow {
  private stopped = false;
  private timer: unknown = null;
  private readonly after: (fn: () => void, ms: number) => unknown;
  private readonly cancel: (h: unknown) => void;
  private readonly now: () => number;

  constructor(private readonly d: CodeFlowDeps) {
    this.after = d.after ?? ((fn, ms) => setTimeout(fn, ms));
    this.cancel = d.cancel ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>));
    this.now = d.now ?? (() => Date.now());
  }

  /** Asks for a code (again: a new one at once). */
  async start(): Promise<void> {
    this.clear();
    if (this.stopped) return;
    this.d.onState({ phase: "asking" });
    const code = await createCode(this.d.fetch, this.d.platform, this.d.appVersion);
    if (this.stopped) return;
    if (!code) {
      this.d.onState({ phase: "down" });
      this.timer = this.after(() => void this.start(), RETRY_DOWN_MS);
      return;
    }
    log("launcher", "cloud code: shown");
    const expiresAt = this.now() + Math.max(60, code.expires_in || 600) * 1000;
    this.d.onState({ phase: "showing", code, expiresAt });
    this.schedule(code, expiresAt, Math.max(1, code.interval || 5));
  }

  stop(): void {
    this.stopped = true;
    this.clear();
  }

  private clear(): void {
    if (this.timer !== null) this.cancel(this.timer);
    this.timer = null;
  }

  private schedule(code: CloudCode, expiresAt: number, intervalSec: number): void {
    this.timer = this.after(() => void this.poll(code, expiresAt, intervalSec), intervalSec * 1000);
  }

  private async poll(code: CloudCode, expiresAt: number, intervalSec: number): Promise<void> {
    this.timer = null;
    if (this.stopped) return;
    if (this.now() >= expiresAt) {
      log("launcher", "cloud code: expired, a new one");
      void this.start();
      return;
    }
    const r = await pollCode(this.d.fetch, code);
    if (this.stopped) return;
    switch (r.kind) {
      case "approved":
        log("launcher", `cloud code: approved for home ${r.approval.home_id}`);
        this.stopped = true;
        this.d.onApproved(r.approval);
        return;
      case "gone":
        log("launcher", "cloud code: gone, a new one");
        void this.start();
        return;
      case "slow_down":
        this.schedule(code, expiresAt, intervalSec + 5);
        return;
      default:
        // pending, or no answer this time: the next poll tries again
        this.schedule(code, expiresAt, intervalSec);
    }
  }
}
