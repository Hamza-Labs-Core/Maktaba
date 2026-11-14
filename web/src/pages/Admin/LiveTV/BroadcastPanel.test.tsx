// The Broadcast panel (Epic 50 story 50.22): hidden without the broadcast flag or for a
// non-admin (403); with livetv.broadcast off it offers to turn it on through the components
// preview; publishing needs the rights tick and shows the API's refusal naming the failed
// check (uplinkLow with its suggestion); a live channel shows its link and Unpublish; the
// uplink is measured on demand. Arabic renders.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";
import { ApiError } from "../../../lib/api";

const { get, post, put, flag } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  flag: { on: true },
}));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});
vi.mock("../../../lib/flags", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/flags")>("../../../lib/flags");
  return { ...actual, useFlag: () => flag.on };
});

import { BroadcastPanel, describeLadder } from "./BroadcastPanel";
import type { BroadcastChannel, BroadcastView } from "../../../lib/tvBroadcastApi";

const channel = (over: Partial<BroadcastChannel> = {}): BroadcastChannel => ({
  id: "c1",
  name: "Seerah",
  number: 101,
  enabled: true,
  configured: true,
  slug: "seerah",
  access: "invite",
  mode: "on_demand",
  rungs: ["src", "720p", "480p"],
  countries: [],
  state: "draft",
  dropped_rungs: 0,
  ...over,
});

const view = (over: Partial<BroadcastView> = {}): BroadcastView => ({
  channels: [channel()],
  uplink: { kbps: 20000, measured_at: "2026-11-14T04:00:00Z" },
  plan: {
    plan: "broadcaster",
    max_channels: 3,
    max_rungs: 3,
    max_top_kbps: 10000,
    access: ["invite", "open"],
    on_demand_only: false,
    agreement_accepted: true,
  },
  cloud: "ok",
  settings: { uplink_share: 0.5, rung_slot_weight: 0.5, allow_software_rungs: false },
  uplink_budget_kbps: 10000,
  uplink_used_kbps: 0,
  rungs: ["src", "720p", "480p", "360p"],
  source_height: 1080,
  checks: ["plan", "agreement", "publishable", "uplink", "encoder", "rights"],
  ...over,
});

function problem(status: number, extra: Record<string, unknown>) {
  return new ApiError({ type: "about:blank", title: "x", status, detail: "", ...extra } as never);
}

function renderPanel() {
  return render(
    <I18nProvider>
      <BroadcastPanel />
    </I18nProvider>
  );
}

beforeEach(() => {
  localStorage.clear();
  get.mockReset();
  post.mockReset();
  put.mockReset();
  flag.on = true;
});

describe("BroadcastPanel", () => {
  it("is hidden without the broadcast flag and asks nothing", () => {
    flag.on = false;
    const { container } = renderPanel();
    expect(container).toBeEmptyDOMElement();
    expect(get).not.toHaveBeenCalled();
  });

  it("is hidden for a non-admin (the API's 403)", async () => {
    get.mockRejectedValue(problem(403, {}));
    const { container } = renderPanel();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("offers to turn broadcasting on while the component is off", async () => {
    get.mockRejectedValueOnce(
      problem(409, { code: "component_off", component: "livetv.broadcast" })
    );
    get.mockResolvedValueOnce(view());
    post.mockImplementation((url: string) =>
      Promise.resolve(
        url.endsWith("/preview") ? { effects: [], conflicts: [], refused: [] } : { ok: true }
      )
    );
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Turn on broadcasting" }));
    expect(post).toHaveBeenCalledWith("/api/components/preview", {
      changes: [{ id: "livetv.broadcast", want: "enabled" }],
    });
    expect(post).toHaveBeenCalledWith("/api/components/apply", {
      changes: [{ id: "livetv.broadcast", want: "enabled" }],
    });
    expect(await screen.findByTestId("broadcast-panel")).toBeInTheDocument();
  });

  it("publishes only with the rights tick and names the failed check", async () => {
    get.mockResolvedValue(view());
    post.mockRejectedValue(
      problem(409, {
        code: "broadcast.uplinkLow",
        check: "uplink",
        params: { kbps: 20000, suggestion: "720p + 480p" },
      })
    );
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Publish this channel" }));
    const publish = screen.getAllByRole("button", { name: "Publish this channel" }).at(-1)!;
    expect(publish).toBeDisabled();
    await userEvent.click(screen.getByLabelText(/I have the rights to broadcast/));
    await userEvent.click(publish);
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/channels/c1/publish", {
      confirm_rights: true,
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Upload: Your internet upload (20.0 Mbps) can't carry this. 720p + 480p fits."
    );
  });

  it("shows a live channel's link and unpublishes it", async () => {
    get.mockResolvedValue(view({ channels: [channel({ state: "live" })] }));
    post.mockResolvedValue(channel());
    renderPanel();
    expect(await screen.findByText("Live")).toBeInTheDocument();
    expect(screen.getByDisplayValue("https://watch.maktaba.tv/seerah")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/channels/c1/unpublish");
  });

  it("measures the upload", async () => {
    get.mockResolvedValue(view({ uplink: { kbps: 0 } }));
    post.mockResolvedValue({ kbps: 30000 });
    renderPanel();
    expect(
      await screen.findByText("Not measured yet. Measure it before publishing.")
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Measure upload" }));
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/speedtest");
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(view());
    renderPanel();
    expect(await screen.findByRole("heading", { name: "البث" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "انشر هذه القناة" })).toBeInTheDocument();
  });

  it("describes a ladder with the channel's height", () => {
    expect(describeLadder(["src", "720p", "480p"], 1080)).toBe("1080p + 720p + 480p");
  });
});
