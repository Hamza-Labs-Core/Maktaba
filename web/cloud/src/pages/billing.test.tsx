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

function routes(me: Record<string, unknown> = {}) {
  return {
    "POST /v1/auth/refresh": session(user()),
    "GET /v1/billing/plans": [
      200,
      {
        plans: [
          plan("free", "Free", 0, false),
          plan("pro", "Pro", 500),
          plan("family", "Family", 900),
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
  it("Upgrade posts checkout and goes to the checkout URL", async () => {
    const f = stubFetch({
      ...routes(),
      "POST /v1/billing/checkout": [
        200,
        { checkout_url: "https://checkout.stripe.test/c/1", session_id: "cs_1" },
      ],
    });
    renderAt("/billing");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Upgrade to Pro" }));
    await waitFor(() =>
      expect(navigateExternal).toHaveBeenCalledWith("https://checkout.stripe.test/c/1")
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
      "POST /v1/billing/portal": [200, { portal_url: "https://billing.stripe.test/p/1" }],
    });
    renderAt("/billing");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Manage billing" }));
    await waitFor(() =>
      expect(navigateExternal).toHaveBeenCalledWith("https://billing.stripe.test/p/1")
    );
  });

  it("shows the return banners", async () => {
    stubFetch(routes());
    renderAt("/billing?billing=ok");
    expect(await screen.findByText(/Your subscription is being activated/)).toBeInTheDocument();
  });
});
