// Epic 58 story 58.09 — signing a TV out in its launcher: the home is sent the refresh token
// (POST /api/auth/logout) with the access token, and the TV forgets its tokens.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render } from "@testing-library/react";
import { AuthProvider, useAuth } from "../lib/auth";
import { setBearerAuth } from "../lib/api";
import { TvSession } from "./session";

let auth: ReturnType<typeof useAuth> | null = null;
function Grab() {
  auth = useAuth();
  return null;
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => {
  setBearerAuth(null);
  localStorage.clear();
  vi.unstubAllGlobals();
  auth = null;
});

describe("a TV's sign-out", () => {
  it("sends the refresh token and forgets the tokens", async () => {
    const s = new TvSession();
    s.save({ access_token: "a1", access_expires_in: 900, refresh_token: "r1" });
    setBearerAuth(s);
    const calls: [string, RequestInit][] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init: RequestInit) => {
        calls.push([url, init]);
        if (url === "/api/auth/me") return json(200, { user: { id: "u1", username: "amal" } });
        return json(200, {});
      })
    );
    render(
      <AuthProvider>
        <Grab />
      </AuthProvider>
    );
    await act(async () => {
      await auth!.logout();
    });
    const out = calls.find(([u]) => u === "/api/auth/logout")!;
    expect(JSON.parse(out[1].body as string)).toEqual({ refresh_token: "r1" });
    expect(new Headers(out[1].headers).get("Authorization")).toBe("Bearer a1");
    expect(s.signedIn()).toBe(false);
    expect(localStorage.getItem("mkt:tv:session")).toBeNull();
  });
});
