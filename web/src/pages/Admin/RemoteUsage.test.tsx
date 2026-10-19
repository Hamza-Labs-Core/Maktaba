import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { I18nProvider } from "../../lib/i18n";
import { RemoteUsage } from "./RemoteUsage";
import type { RemoteControllerStatus } from "../../lib/tvRemoteApi";

const remote: RemoteControllerStatus = {
  ready: true,
  inputs: [],
  pushes: [
    {
      id: "p1",
      channel_id: "6f1c9a2e-0000-4000-8000-000000000101",
      state: "live",
      host_id: "home-1234",
      started_at: "2026-10-19T12:00:00Z",
      viewers: 2,
    },
  ],
  sessions: 2,
  estimate: {
    month: "2026-10",
    delivered_minutes: 3000,
    running_minutes: 0,
    delivery_usd: 3,
    storage_usd: 5,
    total_usd: 8,
    budget_usd: 10,
    percent: 80,
    action: "block_new",
    source: "analytics",
    peak_recording_minutes: 610,
    exhausted: false,
  },
  storage: {
    capacity_minutes: 1000,
    capacity_source: "cloudflare",
    stored_minutes: 610,
    other_minutes: 0,
    running_reserved_minutes: 960,
    pending_recording_minutes: 0,
    running_pushes: 2,
    need_minutes: 480,
    margin_minutes: 30,
    free_minutes: 40,
    fits: false,
    hint: "buy another 1,000-minute block or lower max_push_hours",
  },
  limits: {
    channels_in_use: 2,
    max_channels: 22,
    channels_line: "2 of 22 remote channels in use",
    stored_line: "",
    max_streams_per_user: 2,
    upload_mbps: 13,
  },
};

describe("RemoteUsage", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    get.mockResolvedValue({ integration: {}, remote });
  });

  it("shows the estimate, the limits and the storage guard", async () => {
    render(
      <I18nProvider>
        <RemoteUsage />
      </I18nProvider>
    );
    expect(await screen.findByText(/2 of 22 remote channels in use/)).toBeInTheDocument();
    expect(screen.getByText(/80 % of the \$10.00 budget/)).toBeInTheDocument();
    expect(screen.getByText(/Stored minutes: 610 of 1000/)).toBeInTheDocument();
    expect(screen.getByText(/no room for another broadcast/)).toBeInTheDocument();
    expect(screen.getByText(/no audio-only saving/)).toBeInTheDocument();
  });

  it("stops a push and rotates the signing key after confirmation", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(true);
    post.mockResolvedValue({ key_id: "k2", old_key_id: "k1", old_deleted: true });
    render(
      <I18nProvider>
        <RemoteUsage />
      </I18nProvider>
    );
    await userEvent.click(await screen.findByRole("button", { name: "Stop" }));
    expect(post).toHaveBeenCalledWith("/api/tv/remote/pushes/p1/stop");
    await userEvent.click(screen.getByRole("button", { name: "Rotate signing key" }));
    expect(post).toHaveBeenCalledWith("/api/tv/remote/signing-key/rotate");
    expect(await screen.findByText(/old key is deleted/)).toBeInTheDocument();
  });
});
