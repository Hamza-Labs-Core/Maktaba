// Story 50.32 T07: the user app has no operator console: no Admin link for any account, and
// /admin is the user app's 404 like any unknown path.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { setAccessToken } from "../shared/api";
import { ME, renderAt, session, stubFetch, user } from "../test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => vi.unstubAllGlobals());

describe("the user app has no operator console", () => {
  for (const role of ["user", "service_admin"]) {
    it(`shows no Admin link to a ${role}`, async () => {
      stubFetch({
        "POST /v1/auth/refresh": session(user({ role })),
        "GET /v1/account/me": [200, { ...ME, role }],
        "GET /v1/account/sessions": [200, { sessions: [] }],
      });
      renderAt("/account");
      const nav = await screen.findByRole("navigation", { name: "Main" });
      expect(within(nav).getByRole("link", { name: "Account" })).toBeInTheDocument();
      expect(within(nav).queryByRole("link", { name: "Admin" })).not.toBeInTheDocument();
    });
  }

  it("answers /admin with its 404 page and calls no admin API", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
    });
    renderAt("/admin/accounts");
    expect(
      await screen.findByRole("heading", { name: "This page doesn't exist." })
    ).toBeInTheDocument();
    expect(f.calls.filter((c) => c.path.startsWith("/v1/admin/"))).toEqual([]);
  });
});
