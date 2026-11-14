import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../shared/api";
import { renderAt, session, stubFetch, user } from "../test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => {
  localStorage.removeItem("mkt:cloud:locale");
});

const tier = (id: string, extra: Record<string, unknown>) => ({
  id,
  max_channels: id === "guest" ? 1 : 3,
  max_concurrent_viewers: id === "guest" ? 5 : 100,
  viewer_hours_per_month: id === "guest" ? 50 : 5000,
  max_rungs: id === "guest" ? 2 : 3,
  pay_as_you_go: id !== "guest",
  price_cents: null,
  payg_price_per_100h_cents: null,
  ...extra,
});

const catalog = (prices = false) => ({
  guest: tier("guest", {}),
  broadcaster: tier(
    "broadcaster",
    prices ? { price_cents: 5000, payg_price_per_100h_cents: 600 } : {}
  ),
});

const state = (plan: string, mode: string, minutesFull: number, extra = {}) => ({
  plan,
  mode,
  max_channels: plan === "guest" ? 1 : 3,
  max_concurrent_viewers: plan === "guest" ? 5 : 100,
  usage: { minutes_full: minutesFull, minutes_lowest: 0, minutes_payg: 0 },
  allowance_minutes: plan === "guest" ? 3000 : 300000,
  payg: { on: false, cap_cents: 0, spent_cents: 0 },
  ...extra,
});

function routes(plan: string, view: Record<string, unknown>, prices = false) {
  return {
    "POST /v1/auth/refresh": session(user()),
    "GET /v1/billing/plans": [
      200,
      { payments_enabled: true, plans: [], broadcast: catalog(prices) },
    ] as [number, unknown],
    "GET /v1/billing/me": [
      200,
      { plan, status: "active", cancel_at_period_end: false, has_customer: true },
    ] as [number, unknown],
    "GET /v1/billing/usage": [
      200,
      { month: "2026-11", plan, limit_bytes: 0, used_bytes: 0, servers: [] },
    ] as [number, unknown],
    "GET /v1/homes": [
      200,
      {
        homes: [
          { server_id: "h1", name: "Living room", owned: true },
          { server_id: "h2", name: "A friend's", owned: false },
        ],
      },
    ] as [number, unknown],
    "GET /v1/billing/broadcast": [
      200,
      {
        server_id: "h1",
        tiers: catalog(prices),
        payg: { enabled: false, cap_cents: 0 },
        agreement: { version: 1, accepted: false },
        enabled: true,
        purchasable: prices,
        ...view,
      },
    ] as [number, unknown],
    "POST /v1/broadcast/agreement": [204, null] as [number, unknown],
    "PUT /v1/billing/broadcast": [202, { server_id: "h1", broadcaster: true }] as [number, unknown],
  };
}

// Epic 50 story 50.20 §5: the Broadcaster add-on, guest sharing and each home's viewing.
describe("billing: broadcasting", () => {
  it("offers Broadcaster with its numbers and a price coming soon", async () => {
    stubFetch(routes("free", { state: state("", "off", 0) }));
    renderAt("/billing");
    expect(await screen.findByText(/Publish up to 3 of your channels/)).toBeInTheDocument();
    expect(
      screen.getByText(/up to 100 viewers at once and 5,000 viewer-hours a month/)
    ).toBeInTheDocument();
    expect(screen.getByText(/Guest sharing: invite up to 5 people/)).toBeInTheDocument();
    // a Free account sees that sharing needs Pro, and no home panel
    expect(
      screen.getByText("Sharing channels with people outside your home needs the Pro plan.")
    ).toBeInTheDocument();
    expect(screen.queryByText(/Broadcasting — /)).not.toBeInTheDocument();
  });

  it("asks the Pro owner to accept the agreement, then offers the add-on", async () => {
    const f = stubFetch(routes("pro", { state: state("guest", "full", 1200) }, true));
    renderAt("/billing");
    expect(await screen.findByText("Broadcasting — Living room")).toBeInTheDocument();
    // only owned homes get a panel
    expect(screen.queryByText("Broadcasting — A friend's")).not.toBeInTheDocument();
    expect(screen.getByText("20 of 50 viewer-hours")).toBeInTheDocument();
    expect(screen.getByText("Up to 5 viewers at once")).toBeInTheDocument();
    expect(screen.getByText("Broadcaster agreement")).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Accept the agreement" }));
    await waitFor(() =>
      expect(f.find("POST", "/v1/broadcast/agreement")[0].body).toEqual({ version: 1 })
    );
  });

  it("buys Broadcaster once the agreement is accepted", async () => {
    const f = stubFetch(
      routes(
        "pro",
        { state: state("guest", "full", 0), agreement: { version: 1, accepted: true } },
        true
      )
    );
    renderAt("/billing");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Add Broadcaster" }));
    await waitFor(() =>
      expect(f.find("PUT", "/v1/billing/broadcast")[0].body).toEqual({
        server_id: "h1",
        enabled: true,
      })
    );
  });

  it("says what new viewers get past the allowance, in Arabic too", async () => {
    stubFetch(
      routes(
        "pro",
        {
          state: state("broadcaster", "lowest", 300000),
          agreement: { version: 1, accepted: true },
        },
        true
      )
    );
    renderAt("/billing");
    expect(
      await screen.findByText(
        "New viewers watch in lower quality until next month, or until you raise your limit."
      )
    ).toBeInTheDocument();
    expect(screen.getByText(/at \$6\.00 per 100 viewer-hours/)).toBeInTheDocument();
  });

  it("shows the waiting notice in Arabic", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    stubFetch(
      routes("pro", {
        state: state("broadcaster", "wait", 300000),
        agreement: { version: 1, accepted: true },
      })
    );
    renderAt("/billing");
    expect(await screen.findByText(/المشاهدون الجدد في الانتظار/)).toBeInTheDocument();
  });
});
