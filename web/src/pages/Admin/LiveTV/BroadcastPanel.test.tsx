// The Broadcast panel (Epic 50 story 50.22): hidden without the broadcast flag or for a
// non-admin (403); with livetv.broadcast off it offers to turn it on through the components
// preview; publishing needs the rights tick and shows the API's refusal naming the failed
// check (uplinkLow with its suggestion); a live channel shows its link and Unpublish; the
// uplink is measured on demand. Arabic renders.
//
// Story 50.23: each channel's publishable share and its suggestions (Record permission
// pre-fills the source and opens its About page); the permissions recorded, withdrawn with a
// reason; the publishable-only switch needs a typed confirmation to turn off; a channel's
// countries read as an allow or a deny list.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
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
import type { BroadcastChannel, BroadcastView, PublishableView } from "../../../lib/tvBroadcastApi";

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
  geo_mode: "allow",
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

const pubView = (over: Partial<PublishableView> = {}): PublishableView => ({
  publishable_only: true,
  sources: [],
  shares: [],
  ...over,
});

/** serve answers the panel's two reads: the broadcast view and the publishable view. */
function serve(v: BroadcastView, p: PublishableView = pubView()) {
  get.mockImplementation((url: string) =>
    Promise.resolve(url === "/api/tv/broadcast/publishable" ? p : v)
  );
}

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
    get.mockResolvedValue(pubView());
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
    serve(view());
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
    serve(view({ channels: [channel({ state: "live" })] }));
    post.mockResolvedValue(channel());
    renderPanel();
    expect(await screen.findByText("Live")).toBeInTheDocument();
    expect(screen.getByDisplayValue("https://watch.maktaba.tv/seerah")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Unpublish" }));
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/channels/c1/unpublish");
  });

  it("measures the upload", async () => {
    serve(view({ uplink: { kbps: 0 } }));
    post.mockResolvedValue({ kbps: 30000 });
    renderPanel();
    expect(
      await screen.findByText("Not measured yet. Measure it before publishing.")
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Measure upload" }));
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/speedtest");
  });

  // Story 50.25: what the revocation list does, as the home shows it
  it("names why Maktaba took a channel off air, from the list or the edge", async () => {
    get.mockResolvedValue(
      view({
        channels: [
          channel({ state: "revoked", detail: "list:notice" }),
          channel({ id: "c2", name: "Quran", number: 102, state: "revoked", detail: "incident" }),
          channel({ id: "c3", name: "Kids", number: 103, state: "revoked", detail: "" }),
        ],
      })
    );
    renderPanel();
    expect(
      await screen.findByText(
        "Maktaba took this channel off air: a copyright notice. See your notices on the Broadcast page."
      )
    ).toBeInTheDocument();
    expect(
      screen.getByText(/took this channel off air: a service problem on our side/)
    ).toBeInTheDocument();
    expect(screen.getByText("This channel is off air.")).toBeInTheDocument();
  });

  it("warns while the home's copy of the list is stale, and says when the home is stopped", async () => {
    get.mockResolvedValue(
      view({
        revocations: {
          version: 3,
          stale: true,
          days: 4,
          home: { action: "terminate", reason: "repeat_infringer", kind: "publisher" },
        },
      })
    );
    renderPanel();
    expect(await screen.findByTestId("broadcast-stale")).toHaveTextContent(
      "couldn't check Maktaba's broadcast list for 4 days"
    );
    expect(screen.getByTestId("broadcast-stopped")).toHaveTextContent(
      "Broadcasting from this account has ended: repeated valid copyright notices."
    );
  });

  it("shows a suspension and a publish refused by the list with its reason", async () => {
    get.mockResolvedValue(
      view({
        revocations: {
          version: 3,
          stale: false,
          days: 0,
          home: { action: "suspend", reason: "abuse", kind: "home" },
        },
      })
    );
    post.mockRejectedValue(
      problem(409, { code: "broadcast.revoked.channel", params: { reason: "report" } })
    );
    renderPanel();
    expect(await screen.findByTestId("broadcast-stopped")).toHaveTextContent(
      "Broadcasting from this home is suspended: misuse of the service."
    );
    expect(screen.queryByTestId("broadcast-stale")).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: "Publish this channel" }));
    await userEvent.click(screen.getByLabelText(/I have the rights to broadcast/));
    await userEvent.click(screen.getAllByRole("button", { name: "Publish this channel" }).at(-1)!);
    await waitFor(() =>
      expect(
        screen
          .getAllByRole("alert")
          .some((a) => a.textContent?.includes("took this channel off air: a report we reviewed"))
      ).toBe(true)
    );
  });

  it("renders the list's warnings in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(
      view({
        channels: [channel({ state: "revoked", detail: "list:notice" })],
        revocations: { version: 3, stale: true, days: 4 },
      })
    );
    renderPanel();
    expect(await screen.findByTestId("broadcast-stale")).toHaveTextContent("منذ 4 أيام");
    expect(
      screen.getByText(/أوقفت مكتبة بث هذه القناة: إشعار بشأن حقوق النشر/)
    ).toBeInTheDocument();
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve(view());
    renderPanel();
    expect(await screen.findByRole("heading", { name: "البث" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "انشر هذه القناة" })).toBeInTheDocument();
  });

  it("describes a ladder with the channel's height", () => {
    expect(describeLadder(["src", "720p", "480p"], 1080)).toBe("1080p + 720p + 480p");
  });

  it("shows a channel's publishable share and records permission for a suggestion", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    serve(
      view(),
      pubView({
        shares: [
          {
            channel_id: "c1",
            scheduled_seconds: 7 * 86400,
            publishable_seconds: 7 * 8640,
            percent: 10,
            suggestions: [
              {
                kind: "channel",
                ref: "UCseerah",
                label: "Seerah Talks",
                seconds: 36000,
                url: "https://www.youtube.com/channel/UCseerah/about",
              },
            ],
          },
        ],
      })
    );
    post.mockResolvedValue({ id: "s1" });
    renderPanel();
    expect(await screen.findByText("10% of the next 7 days is publishable")).toBeInTheDocument();
    expect(
      screen.getByText(/Not enough publishable content to fill this channel/)
    ).toBeInTheDocument();
    expect(screen.getByText(/10 h in the next 7 days/)).toBeInTheDocument();
    const row = screen.getByTestId("broadcast-share-c1");
    await userEvent.click(within(row).getByRole("button", { name: "Record permission" }));
    expect(open).toHaveBeenCalledWith(
      "https://www.youtube.com/channel/UCseerah/about",
      "_blank",
      "noopener,noreferrer"
    );
    // pre-filled with the source; the proof link is the admin's to paste
    expect(screen.getByLabelText("Channel, playlist or name")).toHaveValue("UCseerah");
    const save = screen.getAllByRole("button", { name: "Record permission" }).at(-1)!;
    expect(save).toBeDisabled();
    await userEvent.type(
      screen.getByLabelText("Link to where the source allows sharing"),
      "https://www.youtube.com/channel/UCseerah/about"
    );
    await userEvent.click(screen.getByLabelText("The source asks for non-commercial use"));
    expect(screen.getByRole("note")).toHaveTextContent("Maktaba charges for hosting");
    await userEvent.click(save);
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/publishable/sources", {
      kind: "channel",
      ref: "UCseerah",
      label: "Seerah Talks",
      proof_url: "https://www.youtube.com/channel/UCseerah/about",
      note: "",
      credit: "",
      non_commercial: true,
    });
    open.mockRestore();
  });

  it("shows a refusal when the source is already recorded", async () => {
    serve(view());
    post.mockRejectedValue(problem(409, { code: "broadcast.sourceRecorded" }));
    renderPanel();
    const section = await screen.findByTestId("broadcast-publishable");
    await userEvent.click(within(section).getByRole("button", { name: "Record permission" }));
    await userEvent.type(screen.getByLabelText("Channel, playlist or name"), "Sheikh A");
    await userEvent.type(
      screen.getByLabelText("Link to where the source allows sharing"),
      "https://example.org/allow"
    );
    await userEvent.click(screen.getAllByRole("button", { name: "Record permission" }).at(-1)!);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "A permission for this source is already recorded."
    );
  });

  it("lists the permissions recorded and withdraws one with a reason", async () => {
    serve(
      view(),
      pubView({
        sources: [
          {
            id: "s1",
            kind: "speaker",
            ref: "Sheikh A",
            label: "Sheikh A",
            proof_url: "https://example.org/allow",
            note: "",
            credit: "",
            non_commercial: true,
            recorded_by: "Admin",
            recorded_at: "2026-11-14T00:00:00Z",
          },
          {
            id: "s0",
            kind: "channel",
            ref: "UCold",
            label: "Old",
            proof_url: "https://example.org/old",
            note: "",
            credit: "",
            non_commercial: false,
            recorded_by: "Admin",
            recorded_at: "2026-11-01T00:00:00Z",
            revoked_at: "2026-11-02T00:00:00Z",
          },
        ],
      })
    );
    post.mockResolvedValue({});
    renderPanel();
    const src = await screen.findByTestId("broadcast-source-s1");
    expect(src).toHaveTextContent("Speaker or reciter");
    expect(src).toHaveTextContent("This source asks for non-commercial use");
    expect(screen.queryByTestId("broadcast-source-s0")).not.toBeInTheDocument();
    await userEvent.click(within(src).getByRole("button", { name: "Withdraw" }));
    await userEvent.type(screen.getByLabelText("Why (kept with the record)"), "asked to stop");
    await userEvent.click(screen.getAllByRole("button", { name: "Withdraw" }).at(-1)!);
    expect(post).toHaveBeenCalledWith("/api/tv/broadcast/publishable/sources/s1/withdraw", {
      reason: "asked to stop",
    });
  });

  it("turns publishable-only off only with a typed confirmation", async () => {
    serve(view());
    put.mockResolvedValue({ publishable_only: false });
    renderPanel();
    const section = await screen.findByTestId("broadcast-publishable");
    await userEvent.click(within(section).getByRole("button", { name: "Turn off" }));
    const confirm = screen.getAllByRole("button", { name: "Turn off" }).at(-1)!;
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/Type “turn off”/), "turn off");
    await userEvent.click(confirm);
    expect(put).toHaveBeenCalledWith("/api/tv/broadcast/publishable/only", {
      enabled: false,
      confirm: "turn off",
    });
  });

  it("marks a published channel that isn't limited to publishable items", async () => {
    serve(view({ channels: [channel({ state: "live" })] }), pubView({ publishable_only: false }));
    put.mockResolvedValue({ publishable_only: true });
    renderPanel();
    expect(await screen.findByTestId("broadcast-notlimited-c1")).toHaveTextContent(
      "Not limited to publishable items"
    );
    await userEvent.click(screen.getByRole("button", { name: "Turn on" }));
    expect(put).toHaveBeenCalledWith("/api/tv/broadcast/publishable/only", {
      enabled: true,
      confirm: "",
    });
  });

  it("saves a channel's countries as a deny list", async () => {
    serve(view());
    put.mockResolvedValue(channel());
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Set up" }));
    await userEvent.click(screen.getByLabelText("Everywhere except these countries"));
    await userEvent.type(screen.getByLabelText("Countries"), "fr, de");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith(
      "/api/tv/broadcast/channels/c1",
      expect.objectContaining({ countries: ["FR", "DE"], geo_mode: "deny" })
    );
  });

  it("renders the publishable help in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    serve(view());
    renderPanel();
    expect(await screen.findByRole("heading", { name: "ما الذي يمكنني بثه؟" })).toBeInTheDocument();
    expect(screen.getByText(/لا يعني أنه مسموح بإعادة بثه/)).toBeInTheDocument();
  });
});
