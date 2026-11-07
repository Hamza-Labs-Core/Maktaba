import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../api";
import { renderAt, session, stubFetch, user } from "../test/harness";

const { navigateExternal } = vi.hoisted(() => ({ navigateExternal: vi.fn() }));
vi.mock("../nav", () => ({ navigateExternal }));

beforeEach(() => {
  setAccessToken(null);
  navigateExternal.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

const plan = (id: string, name: string, price: number, purchasable = true) => ({
  id,
  name,
  price_cents: price,
  bandwidth_bytes_per_month: 100e9,
  max_servers: 1,
  max_concurrent_streams: 2,
  relay_qos: "standard",
  includes_transcoding: false,
  family_seats: 0,
  purchasable,
});

function routes(me: Record<string, unknown> = {}, paymentsEnabled = true) {
  return {
    "POST /v1/auth/refresh": session(user()),
    "GET /v1/billing/plans": [
      200,
      {
        payments_enabled: paymentsEnabled,
        plans: [
          plan("free", "Free", 0, false),
          plan("pro", "Pro", 500, paymentsEnabled),
          plan("family", "Family", 900, paymentsEnabled),
        ],
      },
    ] as [number, unknown],
    "GET /v1/billing/me": [
      200,
      { plan: "free", status: "active", cancel_at_period_end: false, has_customer: false, ...me },
    ] as [number, unknown],
    "GET /v1/billing/usage": [
      200,
      { month: "2026-10", plan: "free", limit_bytes: 10e9, used_bytes: 2.5e9, servers: [] },
    ] as [number, unknown],
  };
}

describe("billing", () => {
  it("shows paid plans as coming soon while payments are not set up", async () => {
    // a plan an admin set back to free by hand is "inactive"
    const f = stubFetch(routes({ status: "inactive" }, false));
    renderAt("/billing");
    expect(
      await screen.findByText("Paid plans are coming soon: payments are not set up yet.")
    ).toBeInTheDocument();
    expect(screen.getByText("Inactive")).toBeInTheDocument();
    const pro = screen.getByRole("button", { name: "Payments for Pro are coming soon" });
    const family = screen.getByRole("button", { name: "Payments for Family are coming soon" });
    expect(pro).toBeDisabled();
    expect(family).toBeDisabled();
    expect(pro).toHaveTextContent("Payments coming soon");
    expect(screen.queryByRole("button", { name: /Upgrade/ })).not.toBeInTheDocument();
    // the current (free) plan has no button at all
    expect(screen.queryByRole("button", { name: /Free/ })).not.toBeInTheDocument();
    await userEvent.setup().click(pro);
    expect(f.find("POST", "/v1/billing/checkout")).toHaveLength(0);
    expect(navigateExternal).not.toHaveBeenCalled();
  });

  it("says payments are coming soon in Arabic too", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    try {
      stubFetch(routes({}, false));
      renderAt("/billing");
      expect(
        await screen.findByText("الخطط المدفوعة قريبًا: لم يُفعَّل الدفع بعد.")
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "الدفع لخطة Pro قريبًا" })).toBeDisabled();
      expect(screen.getAllByText("الدفع قريبًا")).toHaveLength(2);
    } finally {
      localStorage.removeItem("mkt:cloud:locale");
    }
  });

  it("shows the current plan set by hand, with no manage button", async () => {
    stubFetch(routes({ plan: "pro", status: "active" }, false));
    renderAt("/billing");
    expect(await screen.findByText("Current plan")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Manage billing" })).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Payments for Family are coming soon" })
    ).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "Payments for Pro are coming soon" })
    ).not.toBeInTheDocument();
  });

  it("explains payments_not_set_up if checkout is refused", async () => {
    stubFetch({
      ...routes(),
      "POST /v1/billing/checkout": [503, { error: "payments_not_set_up", message: "x" }],
    });
    renderAt("/billing");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Upgrade to Pro" }));
    expect(await screen.findByText(/Payments are not set up yet/)).toBeInTheDocument();
    expect(navigateExternal).not.toHaveBeenCalled();
  });

  it("Upgrade posts checkout and goes to the checkout URL", async () => {
    const f = stubFetch({
      ...routes(),
      "POST /v1/billing/checkout": [
        200,
        { checkout_url: "https://pay.example.test/checkout/1", session_id: "cs_1" },
      ],
    });
    renderAt("/billing");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Upgrade to Pro" }));
    await waitFor(() =>
      expect(navigateExternal).toHaveBeenCalledWith("https://pay.example.test/checkout/1")
    );
    expect(f.find("POST", "/v1/billing/checkout")[0].body).toEqual({ plan: "pro" });
    // the current (free) plan has no Upgrade button
    expect(screen.queryByRole("button", { name: "Upgrade to Free" })).not.toBeInTheDocument();
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
  });

  it("explains already_subscribed instead of navigating", async () => {
    stubFetch({
      ...routes(),
      "POST /v1/billing/checkout": [409, { error: "already_subscribed", message: "x" }],
    });
    renderAt("/billing");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Upgrade to Family" }));
    expect(await screen.findByText(/Use Manage billing/)).toBeInTheDocument();
    expect(navigateExternal).not.toHaveBeenCalled();
  });

  it("opens the portal with Manage billing", async () => {
    stubFetch({
      ...routes({ plan: "pro", has_customer: true, current_period_end: "2026-11-01T00:00:00Z" }),
      "POST /v1/billing/portal": [200, { portal_url: "https://pay.example.test/portal/1" }],
    });
    renderAt("/billing");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Manage billing" }));
    await waitFor(() =>
      expect(navigateExternal).toHaveBeenCalledWith("https://pay.example.test/portal/1")
    );
  });

  it("shows the return banners", async () => {
    stubFetch(routes());
    renderAt("/billing?billing=ok");
    expect(await screen.findByText(/Your subscription is being activated/)).toBeInTheDocument();
  });

  // Epic 50 story 50.12: the uptime target, plainly a target with no credit
  it("states the uptime target and that there is no credit, with a link to the status page", async () => {
    stubFetch(routes({ status: "inactive" }, false));
    renderAt("/billing");
    expect(
      await screen.findByText(
        /This is a target, not a guarantee: we don't give credits or refunds for downtime\./
      )
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "See how we did on the status page" })).toHaveAttribute(
      "href",
      "https://status.maktaba.tv"
    );
    // nothing else on the page offers one
    const text = document.body.textContent ?? "";
    expect(
      text.replace(/We aim to keep remote access available[^.]*\.[^.]*\.[^.]*downtime\./, "")
    ).not.toMatch(/credit|refund/i);
  });
});
