// Epic 57 story 57.15: Users > the user > Time. The Users page lists the restricted
// accounts the home names (GET /api/parental/children), each with its Time link.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ApiError } from "../../lib/api";
import { ParentalFixture, type MyParental } from "../../lib/parental";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { AdminUsers } from "./Users";

function open(value: Partial<MyParental>, user: User = adminUser) {
  return renderPage(
    <ParentalFixture value={value}>
      <AdminUsers />
    </ParentalFixture>,
    { path: "/admin/users", user }
  );
}

beforeEach(() => {
  get.mockReset();
  get.mockRejectedValue(new ApiError({ type: "about:blank", title: "not found", status: 404 }));
  localStorage.clear();
});

describe("Users: time limits (57.15)", () => {
  it("lists each restricted account with its way to Time", () => {
    open({
      children: [
        { id: "u2", username: "sara", maxTier: 13 },
        { id: "u3", username: "omar", maxTier: 0 },
      ],
    });
    const section = screen.getByRole("region", { name: "Time limits" });
    const links = within(section).getAllByRole("link");
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "/family/u3/time",
      "/family/u2/time",
    ]);
    expect(links[1]).toHaveTextContent("sara");
    expect(links[1]).toHaveTextContent("13+");
  });

  it("says where a restricted account is made when there is none", () => {
    open({});
    const section = screen.getByRole("region", { name: "Time limits" });
    // a home with no iPhone makes one on the web
    expect(within(section).queryByRole("link")).toBeNull();
    expect(section).toHaveTextContent(/on the Family page/);
  });

  it("someone who is not an admin gets none of the page", () => {
    open({ children: [{ id: "u2", username: "sara", maxTier: 13 }] }, memberUser);
    expect(screen.queryByRole("region", { name: "Time limits" })).toBeNull();
    expect(screen.getByText("Admins only")).toBeInTheDocument();
  });
});
