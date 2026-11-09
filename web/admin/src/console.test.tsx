// Story 50.32: the operator console on its own: its catalogs in both languages (T09), the staff
// sign-in when signed out (story 50.33), its own 404, and Arabic right to left.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { CloudApiError, setStaffToken } from "./api";
import { errorMessage } from "@cloud-shared/ui";
import { MESSAGES } from "./catalogs";
import { STAFF_HEADERS } from "./staff";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";
import { renderAt, staffSession, staffSignedOut, stubFetch } from "./test/harness";

beforeEach(() => setStaffToken(null));
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
    const f = stubFetch({ "POST /v1/staff/refresh": staffSignedOut });
    renderAt("/accounts");
    expect(await screen.findByRole("heading", { name: "Staff sign-in" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeInTheDocument();
    // the customer sign-in is gone from the console
    expect(f.calls.filter((c) => c.path.startsWith("/v1/auth/"))).toEqual([]);
    expect(f.calls.filter((c) => c.path.startsWith("/v1/admin/"))).toEqual([]);
  });

  it("answers an unknown path with its own 404", async () => {
    stubFetch({ "POST /v1/staff/refresh": staffSession() });
    renderAt("/nowhere");
    expect(
      await screen.findByRole("heading", { name: "This page doesn't exist." })
    ).toBeInTheDocument();
  });

  it("is right to left in Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubFetch({ "POST /v1/staff/refresh": staffSignedOut });
    renderAt("/");
    expect(await screen.findByRole("heading", { name: "تسجيل دخول الفريق" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "لوحة تشغيل مكتبة" })).toBeInTheDocument();
    await waitFor(() => expect(document.documentElement.dir).toBe("rtl"));
  });

  it("sends its CSRF header on changes and explains a refused one in both languages", () => {
    // the header the admin listener requires (cloud/internal/middleware.StaffHeader)
    expect(STAFF_HEADERS).toEqual({ "X-Maktaba-Staff": "1" });
    const refused = new CloudApiError(
      403,
      "bad_origin",
      "This request didn't come from the console.",
      {}
    );
    for (const lang of ["en", "ar"] as const) {
      const m = MESSAGES[lang];
      const t = (k: string) => m[k] ?? k;
      const has = (k: string) => k in m;
      expect(errorMessage(t as never, has, refused)).toBe(m["admin.badOrigin"]);
    }
  });
});
