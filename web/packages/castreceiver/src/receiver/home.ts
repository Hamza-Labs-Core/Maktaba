// Which address of the home the TV uses (story 58.07, "The home, from the custom data").
//
// The sender hands the address it reached the home on (`home`), and the home's own list
// (`urls`, GET /api/discovery/home's: the LAN ones first, the relay last). The receiver asks
// `home` first and waits at most 3 seconds; then it tries the relay. A page served over HTTPS
// may fetch only HTTPS (mixed content is blocked), so a plain-HTTP address is never tried.
// Playing through the relay shows the Remote badge.

export const HOME_TIMEOUT_MS = 3000;

export interface Discovery {
  id?: string;
  urls?: string[];
  api_level?: number;
}

export interface ResolvedHome {
  /** The base URL every call goes to, without a trailing slash. */
  base: string;
  /** Whether it is the relay (the Remote badge). */
  remote: boolean;
  discovery: Discovery;
}

export class HomeUnreachable extends Error {
  constructor(readonly tried: string[]) {
    super(`the home answered on none of ${tried.length} address(es)`);
    this.name = "HomeUnreachable";
  }
}

/** A base URL without its trailing slash, or "" for one the receiver may not use. */
export function cleanBase(url: unknown): string {
  if (typeof url !== "string") return "";
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return "";
  }
  if (u.protocol !== "https:") return "";
  return `${u.origin}${u.pathname.replace(/\/+$/, "")}`;
}

/** The relay among a discovery list: the last address, when there is more than one and it is
 * not one of the LAN ones before it. */
export function relayOf(urls: readonly string[] | undefined): string {
  const list = (urls ?? []).map(cleanBase).filter(Boolean);
  if (list.length < 2) return "";
  const last = list[list.length - 1];
  return list.slice(0, -1).includes(last) ? "" : last;
}

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

async function discover(fetchFn: Fetch, base: string, timeoutMs: number): Promise<Discovery> {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), timeoutMs);
  try {
    const res = await fetchFn(`${base}/api/discovery/home`, {
      signal: ctl.signal,
      cache: "no-store",
    });
    if (!res.ok) throw new Error(`discovery answered ${res.status}`);
    return (await res.json()) as Discovery;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * The home's address for this cast: `home` when it answers within `timeoutMs`, else the relay
 * from `urls` (the sender's copy of the discovery list), else HomeUnreachable. When `home`
 * answers, its own discovery list names the relay, so `home` being the relay shows the badge.
 */
export async function resolveHome(
  home: string,
  urls: readonly string[] | undefined,
  fetchFn: Fetch = (i, init) => fetch(i, init),
  timeoutMs = HOME_TIMEOUT_MS
): Promise<ResolvedHome> {
  const first = cleanBase(home);
  const tried: string[] = [];
  if (first) {
    tried.push(first);
    try {
      const discovery = await discover(fetchFn, first, timeoutMs);
      const relay = relayOf(discovery.urls) || relayOf(urls);
      return { base: first, remote: relay !== "" && relay === first, discovery };
    } catch {
      // fall through to the relay
    }
  }
  const relay =
    relayOf(urls) ||
    (urls ?? [])
      .map(cleanBase)
      .filter((u) => u && u !== first)
      .pop() ||
    "";
  if (relay && relay !== first) {
    tried.push(relay);
    try {
      const discovery = await discover(fetchFn, relay, timeoutMs);
      return { base: relay, remote: true, discovery };
    } catch {
      // nothing left
    }
  }
  throw new HomeUnreachable(tried);
}
