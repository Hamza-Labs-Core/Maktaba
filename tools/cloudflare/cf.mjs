// A small Cloudflare API client for the cloud's deploy tools (Epic 50, story 50.34). Node 22's
// own fetch, no dependency. The base URL is CLOUDFLARE_API_BASE (tests point it at a local
// fake; the default is Cloudflare's), and nothing here ever prints the token.

export const DEFAULT_BASE = "https://api.cloudflare.com/client/v4";

/** The token for a piece: its own secret (CLOUDFLARE_API_TOKEN_<PIECE>) or the shared one. */
export function tokenFor(env, piece) {
  const own = piece ? env[`CLOUDFLARE_API_TOKEN_${piece.toUpperCase()}`] : "";
  return (own || env.CLOUDFLARE_API_TOKEN || "").trim();
}

export class CloudflareError extends Error {
  constructor(status, path, errors) {
    const first = (errors ?? [])[0];
    super(`Cloudflare ${status} on ${path}${first ? `: ${first.code} ${first.message}` : ""}`);
    this.status = status;
    this.path = path;
    this.errors = errors ?? [];
  }
}

export function client({ token, base = DEFAULT_BASE, fetchImpl = fetch }) {
  if (!token) throw new Error("no Cloudflare API token");
  async function call(method, path, body) {
    const res = await fetchImpl(`${base.replace(/\/$/, "")}${path}`, {
      method,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    let json = null;
    try {
      json = await res.json();
    } catch {
      // not JSON
    }
    if (!res.ok || (json && json.success === false)) throw new CloudflareError(res.status, path, json?.errors);
    return json?.result;
  }
  return {
    get: (p) => call("GET", p),
    put: (p, b) => call("PUT", p, b),
    patch: (p, b) => call("PATCH", p, b),
    post: (p, b) => call("POST", p, b),
    delete: (p) => call("DELETE", p),
    /** The zone's id by name (Zone: Read). */
    async zoneId(name) {
      const r = await call("GET", `/zones?name=${encodeURIComponent(name)}`);
      if (!r?.length) throw new Error(`zone ${name} is not in this account (or the token can't read it)`);
      return r[0].id;
    },
  };
}

/** Masks a value in GitHub Actions' log (the account id), when running there. */
export function mask(value) {
  if (value && process.env.GITHUB_ACTIONS === "true") console.log(`::add-mask::${value}`);
}
