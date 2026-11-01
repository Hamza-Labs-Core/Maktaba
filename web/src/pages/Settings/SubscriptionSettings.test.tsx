// Settings → Subscription cases from web/test-cases/subscription.md.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import { ApiError } from "../../lib/api";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { renderPage, memberUser, adminUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { SubscriptionSettings, type LicenseView } from "./SubscriptionSettings";

const OFF = {
  cloud_relay: false,
  multi_user: false,
  cloud_metadata_backup: false,
  advanced_analytics: false,
  federation: false,
};

const FREE_OVER: LicenseView = {
  available: true,
  tier: "free",
  features: OFF,
  seats_allowed: 1,
  seats_used: 2,
  over_seats: true,
  status: "none",
  message:
    "This home has more users than its plan allows. They all keep working, but no new user can be added until the plan allows more or users are removed.",
  cloud: { claimed: false },
  manage_url: "https://maktaba.tv/billing",
};

const PRO_CLOUD: LicenseView = {
  available: true,
  tier: "pro",
  features: { ...OFF, cloud_relay: true, multi_user: true, federation: true },
  seats_allowed: -1,
  seats_used: 2,
  over_seats: false,
  status: "active",
  license: {
    license_id: "cloud-srv-1",
    tier: "pro",
    seats: 0,
    holder: "owner@example.com",
    issued_at: "2026-10-26T00:00:00Z",
    expires_at: "2026-11-26T00:00:00Z",
    source: "cloud",
  },
  last_refresh_at: "2026-10-26T10:00:00Z",
  cloud: { claimed: true, server_id: "srv-1", slug: "myhome" },
  manage_url: "https://maktaba.tv/billing",
};

function open(user = adminUser) {
  return renderPage(
    <ToastProvider>
      <SubscriptionSettings />
    </ToastProvider>,
    { path: "/settings/subscription", user }
  );
}

const LICENSE = '{"license":{"license_id":"lic_1","tier":"home"},"signature":"abc"}';

describe("Settings → Subscription (test-cases/subscription.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, del]) f.mockReset();
    routeGet(get, {
      "/api/admin/license": FREE_OVER,
      "/api/entitlements": { tier: "free", features: OFF },
    });
  });

  it("SUB-01: Free with two users shows the plan, seats used/allowed and the over-seat warning", async () => {
    open();
    expect(await screen.findByTestId("subscription-seats")).toHaveTextContent("2 of 1");
    expect(screen.getByTestId("subscription-message")).toHaveTextContent("They all keep working");
    expect(screen.getByText("Free")).toBeInTheDocument();
    expect(screen.getByText(/Remote access through Maktaba Cloud/).closest("li")).toHaveTextContent(
      "Not included"
    );
    expect(screen.getByRole("link", { name: "Get a plan" })).toHaveAttribute(
      "href",
      "https://maktaba.tv/billing"
    );
    expect(screen.getByText(/not linked to Maktaba Cloud/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove license" })).toBeNull();
  });

  it("SUB-02: a cloud plan license shows its holder, renewal, unlimited users and the cloud link", async () => {
    routeGet(get, { "/api/admin/license": PRO_CLOUD });
    open();
    expect(await screen.findByTestId("subscription-seats")).toHaveTextContent("2 of unlimited");
    expect(screen.getByText("Pro")).toBeInTheDocument();
    expect(screen.getByText("Active")).toBeInTheDocument();
    expect(screen.getByText("owner@example.com")).toBeInTheDocument();
    expect(screen.getByText("Renews by")).toBeInTheDocument();
    expect(screen.getByText(/linked to Maktaba Cloud as myhome/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Manage plan" })).toBeInTheDocument();
    expect(screen.queryByTestId("subscription-message")).toBeNull();
  });

  it("SUB-03: applying a license sends the parsed JSON, clears the box and shows the new view", async () => {
    post.mockResolvedValue(PRO_CLOUD);
    open();
    const box = await screen.findByLabelText("Paste a license");
    fireEvent.change(box, { target: { value: `  ${LICENSE}\n` } });
    await userEvent.click(screen.getByRole("button", { name: "Apply license" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/admin/license", JSON.parse(LICENSE))
    );
    expect(await screen.findByText("owner@example.com")).toBeInTheDocument();
    expect(box).toHaveValue("");
  });

  it("SUB-04: a refused license shows the server's plain reason", async () => {
    post.mockRejectedValue(
      new ApiError({
        type: "https://maktaba.dev/problems/license-invalid",
        status: 400,
        title: "License not accepted",
        detail: "This license has expired.",
      })
    );
    open();
    fireEvent.change(await screen.findByLabelText("Paste a license"), {
      target: { value: LICENSE },
    });
    await userEvent.click(screen.getByRole("button", { name: "Apply license" }));
    await waitFor(() =>
      expect(
        screen
          .getAllByRole("alert")
          .some((el) => el.textContent?.includes("This license has expired."))
      ).toBe(true)
    );
  });

  it("SUB-05: pasted text that is not JSON never reaches the server", async () => {
    open();
    fireEvent.change(await screen.findByLabelText("Paste a license"), {
      target: { value: "hello" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Apply license" }));
    await waitFor(() =>
      expect(
        screen.getAllByRole("alert").some((el) => el.textContent?.includes("not a Maktaba license"))
      ).toBe(true)
    );
    expect(post).not.toHaveBeenCalled();
  });

  it("SUB-06: removing asks first, then deletes and reloads", async () => {
    routeGet(get, { "/api/admin/license": PRO_CLOUD });
    del.mockResolvedValue(undefined);
    const confirm = vi
      .spyOn(window, "confirm")
      .mockReturnValueOnce(false)
      .mockReturnValueOnce(true);
    open();
    const remove = await screen.findByRole("button", { name: "Remove license" });
    await userEvent.click(remove);
    expect(del).not.toHaveBeenCalled();
    await userEvent.click(remove);
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/admin/license"));
    confirm.mockRestore();
  });

  it("SUB-07: Check now asks the license server and shows the last error", async () => {
    post.mockResolvedValue({
      ...FREE_OVER,
      last_refresh_error: "the license server is unreachable",
    });
    open();
    await userEvent.click(await screen.findByRole("button", { name: "Check now" }));
    expect(post).toHaveBeenCalledWith("/api/admin/license/refresh", {});
    expect(await screen.findByTestId("subscription-refresh-error")).toHaveTextContent(
      "unreachable"
    );
  });

  it("SUB-08: a build without the license key says so and offers no paste box", async () => {
    routeGet(get, { "/api/admin/license": { ...FREE_OVER, available: false } });
    open();
    expect(await screen.findByText(/cannot check licenses/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Paste a license")).toBeNull();
    expect(screen.queryByRole("button", { name: "Check now" })).toBeNull();
  });

  it("SUB-09: a viewer sees the plan read-only and never calls an admin route", async () => {
    open(memberUser);
    expect(await screen.findByText("Your home's admin manages the plan.")).toBeInTheDocument();
    expect(screen.queryByLabelText("Paste a license")).toBeNull();
    expect(get).not.toHaveBeenCalledWith("/api/admin/license");
  });

  it("SUB-10: a revoked license shows the server's message as an alert", async () => {
    routeGet(get, {
      "/api/admin/license": {
        ...PRO_CLOUD,
        tier: "free",
        features: OFF,
        status: "revoked",
        message: "This home's license was revoked by Maktaba, so the home is on the Free plan.",
      },
    });
    open();
    expect(await screen.findByTestId("subscription-message")).toHaveTextContent(
      "revoked by Maktaba"
    );
    expect(screen.getByText("Revoked")).toBeInTheDocument();
  });

  it("SUB-11: a load error offers Retry", async () => {
    get.mockRejectedValueOnce(
      new ApiError({ type: "about:blank", status: 500, title: "Server error" })
    );
    open();
    expect(await screen.findByText("Server error")).toBeInTheDocument();
    routeGet(get, { "/api/admin/license": FREE_OVER });
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByTestId("subscription-seats")).toBeInTheDocument();
  });
});
