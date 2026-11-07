// Answers, refusals and request reading for the store API.

export class Refusal extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly params: Record<string, unknown> = {},
  ) {
    super(code);
  }
}

export const refuse = (status: number, code: string, params: Record<string, unknown> = {}): never => {
  throw new Refusal(status, code, params);
};

const SECURITY = {
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "same-origin",
  "Strict-Transport-Security": "max-age=63072000; includeSubDomains",
};

export function json(body: unknown, status = 200, headers: HeadersInit = {}): Response {
  const h = new Headers(headers);
  h.set("Content-Type", "application/json; charset=utf-8");
  h.set("Cache-Control", "no-store");
  for (const [k, v] of Object.entries(SECURITY)) h.set(k, v);
  return new Response(JSON.stringify(body), { status, headers: h });
}

export function problem(r: Refusal): Response {
  return json({ error: r.code, ...r.params }, r.status);
}

/** A JSON body of at most max bytes (64 KB by default). */
export async function body<T = Record<string, unknown>>(req: Request, max = 64 << 10): Promise<T> {
  const len = Number(req.headers.get("Content-Length") ?? "0");
  if (len > max) refuse(413, "body_too_large");
  const text = await req.text();
  if (text.length > max) refuse(413, "body_too_large");
  if (!text) return {} as T;
  try {
    const v = JSON.parse(text);
    if (v === null || typeof v !== "object" || Array.isArray(v)) refuse(400, "body_not_object");
    return v as T;
  } catch (e) {
    if (e instanceof Refusal) throw e;
    return refuse(400, "body_not_json");
  }
}

export function str(v: unknown, field: string, max = 200): string {
  if (typeof v !== "string" || v.length === 0 || v.length > max) refuse(400, "field_invalid", { field });
  return v as string;
}

export function optStr(v: unknown, field: string, max = 200): string | undefined {
  if (v === undefined || v === null || v === "") return undefined;
  return str(v, field, max);
}

/** The client's address and its prefix (/24 for IPv4, /64 for IPv6): limits and the audit. */
export function clientIP(req: Request): { ip: string; prefix: string } {
  const ip = req.headers.get("CF-Connecting-IP") ?? "127.0.0.1";
  if (ip.includes(":")) {
    const parts = ip.split(":");
    return { ip, prefix: parts.slice(0, 4).join(":") + "::/64" };
  }
  const p = ip.split(".");
  return { ip, prefix: `${p[0]}.${p[1]}.${p[2]}.0/24` };
}

export function cookie(req: Request, name: string): string | undefined {
  const raw = req.headers.get("Cookie") ?? "";
  for (const part of raw.split(";")) {
    const [k, ...rest] = part.trim().split("=");
    if (k === name) return rest.join("=");
  }
  return undefined;
}
