import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../shared/api";
import { renderAt, session, stubFetch, user } from "../test/harness";

const { navigateExternal } = vi.hoisted(() => ({ navigateExternal: vi.fn() }));
vi.mock("../nav", () => ({ navigateExternal }));

beforeEach(() => {
  setAccessToken(null);
  navigateExternal.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

const plan = (id: string, name: string, price: number | null, purchasable = true) => ({
  id,
  name,
  price_cents: price,
  relay: id !== "free",
  relay_cap_kbps: id === "basic" ? 3000 : 0,
  max_relay_streams: id === "basic" ? 2 : null,
  fair_use_bytes_per_month: id === "free" ? 0 : 500e9,
  allowance_bytes_per_month: null,
  pay_as_you_go: id === "pro",
  extra_streams: id === "pro",
  max_servers: 1,
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
          plan("basic", "Basic", 500, paymentsEnabled),
          plan("pro", "Pro", 900, paymentsEnabled),
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
    const basic = screen.getByRole("button", { name: "Payments for Basic are coming soon" });
    const pro = screen.getByRole("button", { name: "Payments for Pro are coming soon" });
    expect(pro).toBeDisabled();
    expect(basic).toBeDisabled();
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
      expect(screen.getByRole("button", { name: "الدفع لخطة Basic قريبًا" })).toBeDisabled();
      expect(screen.getAllByText("الدفع قريبًا")).toHaveLength(2);
    } finally {
      localStorage.removeItem("mkt:cloud:locale");
    }
  });

  it("shows the current plan set by hand, with no manage button", async () => {
    stubFetch(routes({ plan: "basic", status: "active" }, false));
    renderAt("/billing");
    expect(await screen.findByText("Current plan")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Manage billing" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Payments for Pro are coming soon" })).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "Payments for Basic are coming soon" })
    ).not.toBeInTheDocument();
  });

  it("explains payments_not_set_up if checkout is refused", async () => {
    stubFetch({
      ...routes(),
      "POST /v1/billing/checkout": [503, { error: "payments_not_set_up", message: "x" }],
    });
    renderAt("/billing");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Upgrade to Basic" }));
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
    await u.click(await screen.findByRole("button", { name: "Upgrade to Basic" }));
    await waitFor(() =>
      expect(navigateExternal).toHaveBeenCalledWith("https://pay.example.test/checkout/1")
    );
    expect(f.find("POST", "/v1/billing/checkout")[0].body).toEqual({ plan: "basic" });
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
    await userEvent.setup().click(await screen.findByRole("button", { name: "Upgrade to Pro" }));
    expect(await screen.findByText(/Use Manage billing/)).toBeInTheDocument();
    expect(navigateExternal).not.toHaveBeenCalled();
  });

  it("opens the portal with Manage billing", async () => {
    stubFetch({
      ...routes({ plan: "basic", has_customer: true, current_period_end: "2026-11-01T00:00:00Z" }),
      "POST /v1/billing/portal": [200, { portal_url: "https://pay.example.test/portal/1" }],
    });
    renderAt("/billing");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Manage billing" }));
    await waitFor(() =>
      expect(navigateExternal).toHaveBeenCalledWith("https://pay.example.test/portal/1")
    );
  });

  // Epic 50 story 50.19: the three plans' sentences; a price not set yet is "coming soon", and
  // Pro's sentence names its allowance and devices only once they are set.
  it("describes Free, Basic and Pro, with prices not set yet", async () => {
    stubFetch({
      ...routes({}, false),
      "GET /v1/billing/plans": [
        200,
        {
          payments_enabled: false,
          plans: [
            plan("free", "Free", 0, false),
            plan("basic", "Basic", null, false),
            plan("pro", "Pro", null, false),
          ],
        },
      ],
    });
    renderAt("/billing");
    expect(await screen.findByText(/Play at home, and from anywhere directly/)).toBeInTheDocument();
    expect(
      screen.getByText(/at up to about 3 Mbps \(good 720p\), on up to 2 devices/)
    ).toBeInTheDocument();
    expect(screen.getByText(/Fair use: after 500 GB in a month/)).toBeInTheDocument();
    expect(screen.getByText(/at full quality\. After its monthly allowance/)).toBeInTheDocument();
    expect(screen.getAllByText("Price coming soon")).toHaveLength(2);
  });

  // Epic 50 story 50.19 §4–§6: use by quality; Pro's pay-as-you-go needs a limit, shows what
  // it spent and says when 80 % is used.
  it("lets a Pro owner turn on pay as you go with a monthly limit", async () => {
    const f = stubFetch({
      ...routes({ plan: "pro", status: "active" }),
      "GET /v1/billing/usage": [
        200,
        {
          month: "2026-11",
          plan: "pro",
          limit_bytes: 0,
          used_bytes: 9e9,
          servers: [],
          by_mode: { full: 4e9, payg: 1e9, capped: 4e9, capped_slow: 0 },
        },
      ],
      "GET /v1/billing/payg": [
        200,
        { enabled: true, cap_cents: 1000, spent_cents: 850, price_per_gb_cents: 10 },
      ],
      "PATCH /v1/billing/payg": [
        200,
        { enabled: false, cap_cents: 1000, spent_cents: 850, price_per_gb_cents: 10 },
      ],
    });
    renderAt("/billing");
    expect(await screen.findByText("Pay as you go")).toBeInTheDocument();
    expect(screen.getByText(/Full quality: /)).toBeInTheDocument();
    expect(screen.getByText(/Capped quality \(about 3 Mbps\): /)).toBeInTheDocument();
    expect(
      screen.getByText("You've used 80% of your pay-as-you-go limit this month.")
    ).toBeInTheDocument();
    expect(screen.getByText(/\$8\.50 of \$10\.00 spent this month/)).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Turn off" }));
    await waitFor(() =>
      expect(f.find("PATCH", "/v1/billing/payg")[0].body).toEqual({
        enabled: false,
        cap_cents: 1000,
      })
    );
  });

  it("has no pay as you go before its price is set", async () => {
    stubFetch({
      ...routes({ plan: "pro", status: "active" }),
      "GET /v1/billing/payg": [
        200,
        { enabled: false, cap_cents: 0, spent_cents: 0, price_per_gb_cents: null },
      ],
    });
    renderAt("/billing");
    expect(await screen.findByText("Current plan")).toBeInTheDocument();
    expect(screen.queryByText("Pay as you go")).not.toBeInTheDocument();
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
