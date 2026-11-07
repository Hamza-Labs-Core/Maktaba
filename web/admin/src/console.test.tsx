// Story 50.32: the operator console on its own: its catalogs in both languages (T09), a sign-in
// when signed out, its own 404, and Arabic right to left.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { setAccessToken } from "@cloud-shared/api";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";
import { renderAt, session, signedOut, stubFetch, user } from "./test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("the operator console", () => {
  it("has every key in English and Arabic, with the same placeholders", () => {
    expect(Object.keys(ar).sort()).toEqual(Object.keys(en).sort());
    const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort();
    const arMap = ar as Record<string, string>;
    for (const [k, v] of Object.entries(en as Record<string, string>)) {
      expect([k, vars(arMap[k])]).toEqual([k, vars(v)]);
    }
  });

  it("asks a signed-out visitor to sign in and calls no admin API", async () => {
    const f = stubFetch({ "POST /v1/auth/refresh": signedOut });
    renderAt("/accounts");
    expect(
      await screen.findByRole("heading", { name: "Maktaba operator console" })
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
    expect(f.calls.filter((c) => c.path.startsWith("/v1/admin/"))).toEqual([]);
  });

  it("answers an unknown path with its own 404", async () => {
    stubFetch({ "POST /v1/auth/refresh": session(user({ role: "service_admin" })) });
    renderAt("/nowhere");
    expect(
      await screen.findByRole("heading", { name: "This page doesn't exist." })
    ).toBeInTheDocument();
  });

  it("is right to left in Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubFetch({ "POST /v1/auth/refresh": signedOut });
    renderAt("/");
    expect(await screen.findByRole("heading", { name: "لوحة تشغيل مكتبة" })).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
  });
});
