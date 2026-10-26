// Test helpers for the cloud app: a routed fetch stub and a renderer that
// mounts the real providers and routes at a given URL.
import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { vi } from "vitest";
import { AppRoutes, Providers } from "../App";
import type { CloudUser } from "../api";

export interface Call {
  method: string;
  path: string;
  body: unknown;
  headers: Record<string, string>;
}

export type Handler = (call: Call, n: number) => Response | Promise<Response>;

export function json(status: number, body?: unknown, headers: Record<string, string> = {}) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

/**
 * Stubs global fetch. Routes are keyed "METHOD /path" (query ignored) or
 * "METHOD /path?query" (exact); a value is a Handler or a fixed [status, body].
 * Unrouted calls answer 404.
 */
export function stubFetch(routes: Record<string, Handler | [number, unknown?]>) {
  const calls: Call[] = [];
  const seen = new Map<string, number>();
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    let body: unknown;
    if (typeof init?.body === "string") {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    const call: Call = {
      method,
      path,
      body,
      headers: (init?.headers ?? {}) as Record<string, string>,
    };
    calls.push(call);
    const exact = `${method} ${path}`;
    const bare = `${method} ${path.split("?")[0]}`;
    const key = exact in routes ? exact : bare;
    const route = routes[key];
    const n = (seen.get(key) ?? 0) + 1;
    seen.set(key, n);
    if (!route) return json(404, { error: "not_found", message: `unrouted ${exact}` });
    if (typeof route === "function") return route(call, n);
    return json(route[0], route[1]);
  });
  vi.stubGlobal("fetch", fn);
  return {
    fn,
    calls,
    find: (method: string, p: string) =>
      calls.filter((c) => c.method === method && c.path.split("?")[0] === p),
  };
}

export function user(overrides: Partial<CloudUser> = {}): CloudUser {
  return {
    id: "u1",
    email: "sam@example.com",
    email_verified: true,
    display_name: "Sam",
    plan: "free",
    role: "user",
    ...overrides,
  };
}

/** A refresh route that restores a session for `u`. */
export function session(u: CloudUser): [number, unknown] {
  return [200, { access_token: "tok", expires_in: 900, user: u }];
}

export const signedOut: [number, unknown] = [401, { error: "no_session", message: "no session" }];

export function renderAt(path: string) {
  return render(
    <Providers>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
      </MemoryRouter>
    </Providers>
  );
}

export const ME = {
  id: "u1",
  email: "sam@example.com",
  email_verified: true,
  display_name: "Sam",
  locale: "en",
  plan: "free",
  role: "user",
  created_at: "2026-01-01T00:00:00Z",
  identities: [],
  has_password: true,
};
