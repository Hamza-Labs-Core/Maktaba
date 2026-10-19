// Simulcast (story 32.13, TC8): the destinations (key masked), the month's
// output-hours, per-output status with Start now / Stop now, adding a
// destination (YouTube/Facebook URLs prefilled, key sent once), and the
// channel editor whose turning-on asks for the rights confirmation before
// it PUTs rights_confirmed; a 409 rights_confirmation_required from the
// server opens the same dialog. Arabic renders.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../../lib/i18n";
import { ApiError } from "../../../lib/api";

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, delete: del } };
});

import { SimulcastPanel } from "./Simulcast";
import type { SimulcastView } from "../../../lib/tvSimulcastApi";

const channels = {
  items: [
    { id: "c1", number: 101, name: "Seerah" },
    { id: "c2", number: 102, name: "Fiqh" },
  ],
};

function view(over: Partial<SimulcastView> = {}): SimulcastView {
  return {
    enabled: true,
    ready: true,
    settings: {
      max_outputs_per_channel: 3,
      max_hours_per_month: 60,
      lead_seconds: 60,
      tail_seconds: 30,
    },
    destinations: [
      {
        id: "d1",
        name: "My YouTube",
        kind: "youtube",
        ingest_url: "rtmp://a.rtmp.youtube.com/live2",
        key_set: true,
        stream_key_masked: "••••wxyz",
        in_use: true,
      },
    ],
    attachments: [
      {
        id: "a1",
        channel_id: "c1",
        destination_id: "d1",
        mode: "always",
        rules: [],
        enabled: true,
        rights_confirmed_by: "admin",
        rights_confirmed_at: "2026-10-19T10:00:00Z",
        channel_name: "Seerah",
        channel_number: 101,
        destination_name: "My YouTube",
        destination_kind: "youtube",
        output: { state: "live", platform_note: true, minutes: 12 },
      },
    ],
    usage: {
      month: "2026-10",
      minutes: 750,
      hours: 12.5,
      cap_hours: 60,
      percent: 20.8,
      capped: false,
      cost_usd: 0.75,
      budget_exhausted: false,
    },
    default_ingest: {
      youtube: "rtmp://a.rtmp.youtube.com/live2",
      facebook: "rtmps://live-api-s.facebook.com:443/rtmp/",
    },
    ...over,
  };
}

function setup(v: SimulcastView = view()) {
  get.mockImplementation((path: string) =>
    Promise.resolve(path.startsWith("/api/channels") && !path.includes("simulcast") ? channels : v)
  );
}

function renderPanel() {
  render(
    <I18nProvider>
      <SimulcastPanel />
    </I18nProvider>
  );
}

describe("SimulcastPanel", () => {
  beforeEach(() => {
    localStorage.clear();
    for (const f of [get, post, put, del]) f.mockReset();
    post.mockResolvedValue(undefined);
    put.mockResolvedValue(view());
  });

  it("lists destinations masked, the usage and each output's status", async () => {
    setup();
    renderPanel();
    expect(await screen.findByRole("heading", { name: "Simulcast" })).toBeInTheDocument();
    expect(screen.getByText("••••wxyz")).toBeInTheDocument();
    expect(screen.getByText(/12\.5 of 60 output-hours/)).toBeInTheDocument();
    const table = screen.getByRole("table", { name: "Outputs" });
    const row = within(table).getAllByRole("row")[1];
    expect(within(row).getByText("Live")).toBeInTheDocument();
    expect(
      within(row).getByText(/destination's own state is shown on the platform/)
    ).toBeInTheDocument();
    // a destination in use cannot be deleted
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
  });

  it("shows the 80 % and 100 % cap notices (32.13 AC9)", async () => {
    setup(view({ notice: { month: "2026-10", level: 80, hours: 48, cap_hours: 60 } }));
    const { unmount } = render(
      <I18nProvider>
        <SimulcastPanel />
      </I18nProvider>
    );
    expect(await screen.findByTestId("simulcast-cap-notice")).toHaveTextContent(
      "simulcast used 80% of the monthly output hours (48 of 60 h)"
    );
    unmount();
    setup(view({ notice: { month: "2026-10", level: 100, hours: 60, cap_hours: 60 } }));
    renderPanel();
    expect(await screen.findByTestId("simulcast-cap-notice")).toHaveTextContent(
      /used all of the monthly output hours \(60 of 60 h\)\. Outputs stop/
    );
  });

  it("shows no cap notice without one", async () => {
    setup();
    renderPanel();
    await screen.findByRole("heading", { name: "Simulcast" });
    expect(screen.queryByTestId("simulcast-cap-notice")).toBeNull();
  });

  it("stops and starts an output", async () => {
    const user = userEvent.setup();
    setup();
    renderPanel();
    await user.click(await screen.findByRole("button", { name: "Stop now" }));
    expect(post).toHaveBeenCalledWith("/api/tv/simulcast/outputs/a1/stop");
    setup(
      view({
        attachments: [
          { ...view().attachments[0], output: { state: "off", platform_note: false, minutes: 0 } },
        ],
      })
    );
    await user.click(await screen.findByRole("button", { name: "Stop now" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Start now" })).toBeInTheDocument()
    );
    await user.click(screen.getByRole("button", { name: "Start now" }));
    expect(post).toHaveBeenCalledWith("/api/tv/simulcast/outputs/a1/start");
  });

  it("adds a destination with the platform's URL prefilled", async () => {
    const user = userEvent.setup();
    setup(view({ destinations: [], attachments: [] }));
    post.mockResolvedValue({ id: "d2" });
    renderPanel();
    await user.click(await screen.findByRole("button", { name: "Add destination" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "Page");
    await user.selectOptions(within(dialog).getByLabelText("Kind"), "facebook");
    expect(within(dialog).getByLabelText("Ingest URL")).toHaveValue(
      "rtmps://live-api-s.facebook.com:443/rtmp/"
    );
    const save = within(dialog).getByRole("button", { name: "Save" });
    expect(save).toBeDisabled(); // a new destination needs its key
    await user.type(within(dialog).getByLabelText("Stream key"), "FB-1234-abcd");
    await user.click(save);
    expect(post).toHaveBeenCalledWith("/api/tv/simulcast/destinations", {
      name: "Page",
      kind: "facebook",
      ingest_url: "rtmps://live-api-s.facebook.com:443/rtmp/",
      stream_key: "FB-1234-abcd",
    });
  });

  it("asks for the rights confirmation before turning an attachment on", async () => {
    const user = userEvent.setup();
    setup(view({ attachments: [] }));
    renderPanel();
    await user.selectOptions(await screen.findByLabelText("Channel"), "c2");
    await user.click(screen.getByRole("button", { name: "Simulcast…" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByLabelText(/My YouTube/));
    await user.click(within(dialog).getByLabelText("On"));
    await user.selectOptions(within(dialog).getByLabelText("When"), "schedule");
    // a schedule needs a rule
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: "Add rule" }));
    await user.click(within(dialog).getByLabelText("Fri"));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    const rights = await screen.findByRole("dialog");
    expect(rights).toHaveTextContent("copyright claims");
    expect(rights).toHaveTextContent("Fiqh");
    expect(put).not.toHaveBeenCalled();
    await user.click(within(rights).getByRole("button", { name: /I hold the rights/ }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/channels/c2/simulcast", [
        {
          destination_id: "d1",
          mode: "schedule",
          rules: [{ kind: "window", days: [5], start: "20:00", end: "22:00" }],
          enabled: true,
          rights_confirmed: true,
        },
      ])
    );
  });

  it("opens the rights dialog on the server's 409", async () => {
    const user = userEvent.setup();
    // the page thinks the attachment is on already; the server says it needs a confirmation
    setup();
    put.mockRejectedValueOnce(
      new ApiError({
        type: "https://maktaba.dev/problems/rights-confirmation-required",
        title: "Conflict",
        status: 409,
        code: "rights_confirmation_required",
      } as never)
    );
    renderPanel();
    const table = await screen.findByRole("table", { name: "Outputs" });
    await user.click(within(table).getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledTimes(1);
    expect(await screen.findByText(/copyright claims/)).toBeInTheDocument();
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    setup();
    renderPanel();
    expect(await screen.findByRole("heading", { name: "البث المتزامن" })).toBeInTheDocument();
    expect(screen.getByText("على الهواء")).toBeInTheDocument();
  });
});
