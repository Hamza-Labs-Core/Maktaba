// Epic 57 family settings on the web: the Family page, as an admin, a guardian and
// a viewer meet it. The home decides who looks after whom; the page shows its
// answer, and only an admin is offered a way to make an account restricted.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ParentalFixture, type MyParental } from "../../lib/parental";
import { forgetAnswers } from "../../lib/pageCache";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import type { User } from "../../lib/auth";
import { Family } from "./Family";
import { CHILD, familyGet, problem, type FamilyHome } from "./familyHome";

const guardian: User = { id: "u-guardian", username: "mum", is_admin: false };

function open(
  h: FamilyHome,
  user: User = guardian,
  value: Partial<MyParental> = {},
  locale: "en" | "ar" = "en"
) {
  localStorage.setItem("mkt:locale", locale);
  get.mockImplementation(familyGet(h));
  return renderPage(
    <ParentalFixture value={value}>
      <Family />
    </ParentalFixture>,
    {
      path: "/family",
      user,
      extraRoutes: <Route path="/family/:userId" element={<p>the account page</p>} />,
    }
  );
}

const two = [
  { id: "u2", username: "omar", max_tier: 0 },
  { id: CHILD, username: "sara", max_tier: 13 },
];

beforeEach(() => {
  get.mockReset();
  localStorage.clear();
  forgetAnswers("parental:");
});

describe("Family", () => {
  it("lists the accounts the home says this person looks after, each with its page", async () => {
    open({ children: two });
    const list = await screen.findByTestId("parental-children");
    const links = within(list).getAllByRole("link");
    expect(links.map((a) => a.getAttribute("href"))).toEqual(["/family/u2", `/family/${CHILD}`]);
    expect(links[0]).toHaveTextContent("omar");
    expect(links[0]).toHaveTextContent("All ages");
    expect(links[1]).toHaveTextContent("13+");
  });

  it("a guardian gets Requests and Parent PIN, and no way to make an account restricted", async () => {
    open({ children: two });
    await screen.findByTestId("parental-children");
    expect(screen.getByRole("link", { name: "Requests" })).toHaveAttribute(
      "href",
      "/family/requests"
    );
    expect(screen.getByRole("link", { name: "Parent PIN" })).toHaveAttribute("href", "/family/pin");
    expect(screen.queryByTestId("family-make")).toBeNull();
    // the accounts list is the admin's: a guardian's page never asks for it
    expect(get.mock.calls.map((c) => c[0])).not.toContain("/api/admin/library-acl");
  });

  it("shows how many requests wait", async () => {
    open({ children: two }, guardian, { pendingRequests: 3 });
    const count = await screen.findByTestId("family-requests-count");
    expect(count).toHaveTextContent("3");
    expect(count).toHaveAccessibleName("Requests waiting: 3");
  });

  it("someone who looks after no one sees an empty list and nothing to manage", async () => {
    open({ children: [] }, memberUser);
    expect(await screen.findByText("No restricted accounts")).toBeInTheDocument();
    expect(screen.queryByTestId("parental-children")).toBeNull();
    expect(screen.queryByTestId("family-links")).toBeNull();
    expect(screen.queryByTestId("family-make")).toBeNull();
  });

  it("an admin picks an unrestricted account and goes to its page", async () => {
    open(
      {
        children: [{ id: CHILD, username: "sara", max_tier: 13 }],
        accounts: [
          { id: adminUser.id, username: "admin" },
          { id: CHILD, username: "sara" },
          { id: "u9", username: "yusuf" },
        ],
      },
      adminUser
    );
    const make = await screen.findByTestId("family-make");
    const select = within(make).getByLabelText("Account");
    // not the admin themselves, not an account that is restricted already
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent)
    ).toEqual(["Choose an account", "yusuf"]);
    expect(within(make).getByRole("button", { name: "Continue" })).toBeDisabled();
    await userEvent.selectOptions(select, "u9");
    await userEvent.click(within(make).getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("the account page")).toBeInTheDocument();
  });

  it("an admin with no other account is sent to Users", async () => {
    open({ children: [], accounts: [{ id: adminUser.id, username: "admin" }] }, adminUser);
    const make = await screen.findByTestId("family-make");
    expect(within(make).getByRole("link", { name: "Users" })).toHaveAttribute(
      "href",
      "/admin/users"
    );
  });

  it("shows the home's failure", async () => {
    get.mockRejectedValue(problem(500));
    localStorage.setItem("mkt:locale", "en");
    renderPage(<Family />, { path: "/family", user: guardian });
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });

  it("is in Arabic, right to left", async () => {
    open({ children: two }, guardian, { pendingRequests: 2 }, "ar");
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("العائلة");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("link", { name: /الطلبات/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "رمز الوالدين" })).toBeInTheDocument();
    expect(screen.getByTestId("family-requests-count")).toHaveTextContent("٢");
  });
});
