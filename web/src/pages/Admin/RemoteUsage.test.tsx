import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { I18nProvider } from "../../lib/i18n";
import { RemoteUsage } from "./RemoteUsage";
import type { RemoteControllerStatus } from "../../lib/tvRemoteApi";
import { type Deferred, deferred } from "../../test/deferred";

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

  it("a push stopped stays stopped when a read begun before the stop lands last", async () => {
    const two = {
      ...remote,
      pushes: [
        remote.pushes[0]!,
        { ...remote.pushes[0]!, id: "p2", channel_id: "7a2d0b3f-0000-4000-8000-000000000102" },
      ],
    };
    const reads: Deferred[] = [];
    get
      .mockResolvedValueOnce({ integration: {}, remote: two })
      .mockImplementationOnce(() => {
        const d = deferred();
        reads.push(d);
        return d.promise;
      })
      .mockResolvedValue({ integration: {}, remote: { ...two, pushes: [] } });
    post.mockResolvedValue(undefined);
    render(
      <I18nProvider>
        <RemoteUsage />
      </I18nProvider>
    );
    const user = userEvent.setup();
    await screen.findByText("6f1c9a2e");
    // p1 stopped: the list is read again, and that read is held
    await user.click(screen.getAllByRole("button", { name: "Stop" })[0]!);
    await waitFor(() => expect(reads).toHaveLength(1));
    // p2 stopped: the read after it answers with neither running
    await user.click(screen.getAllByRole("button", { name: "Stop" })[1]!);
    expect(post).toHaveBeenCalledWith("/api/tv/remote/pushes/p2/stop");
    await waitFor(() => expect(screen.queryByText("7a2d0b3f")).toBeNull());
    await act(async () => reads[0]!.resolve({ integration: {}, remote: two }));
    expect(screen.queryByText("7a2d0b3f")).toBeNull();
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
