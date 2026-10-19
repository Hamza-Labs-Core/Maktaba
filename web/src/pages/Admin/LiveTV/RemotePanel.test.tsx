// Story 32.11 TC4 — the Remote panel renders each state; Arabic.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { I18nProvider } from "../../../lib/i18n";
import { RemotePanel, REMOTE_PAGE } from "./RemotePanel";

const integration = (over: Record<string, unknown> = {}) => ({
  configured: true,
  account_id: "acc",
  token_set: true,
  token_problem: null,
  signing_key_set: true,
  customer_domain: "customer-abc.cloudflarestream.com",
  enabled: true,
  can_enable: true,
  last_test: null,
  ...over,
});

const estimate = {
  month: "2026-10",
  delivered_minutes: 120,
  running_minutes: 30,
  delivery_usd: 0.15,
  storage_usd: 5,
  total_usd: 5.15,
  budget_usd: 10,
  percent: 51.5,
  action: "block_new",
  source: "analytics",
  peak_recording_minutes: 200,
  exhausted: false,
};

const remote = (over: Record<string, unknown> = {}) => ({
  ready: true,
  inputs: [
    {
      id: "i1",
      cf_uid: "cf1",
      name: "maktaba-1a2b-1",
      state: "broadcasting",
      channel_id: "c1aaaaaa-0000",
      assigned_at: "2026-10-19T10:00:00Z",
    },
  ],
  pushes: [
    {
      id: "p1",
      channel_id: "c1",
      channel_number: 105,
      channel_name: "Quran",
      state: "live",
      host_id: "home-host-1",
      started_at: "2026-10-19T10:00:00Z",
      live_at: "2026-10-19T10:00:10Z",
      viewers: 2,
      cf_viewers: 3,
    },
    {
      id: "p2",
      channel_id: "c2",
      channel_number: 106,
      channel_name: "Kids",
      state: "failed",
      started_at: "2026-10-19T09:00:00Z",
      detail: "Cloudflare: the video is not H.264 (ERR_UNSUPPORTED_VIDEO_CODEC)",
      viewers: 0,
      cf_viewers: null,
    },
  ],
  sessions: 2,
  session_list: [
    {
      id: "s1",
      user: "amina",
      device: "Safari on iOS",
      channel_id: "c1",
      channel_number: 105,
      channel_name: "Quran",
      started_at: "2026-10-19T10:01:00Z",
      last_beat_at: "2026-10-19T10:05:00Z",
    },
  ],
  events: [
    {
      at: "2026-10-19T10:00:10Z",
      level: "INFO",
      event: "tv_push_live",
      message: "tv_remote: push live",
      push_id: "p1",
    },
    {
      at: "2026-10-19T09:00:30Z",
      level: "ERROR",
      event: "tv_push_error",
      message: "tv_remote: push failed",
      code: "ERR_UNSUPPORTED_VIDEO_CODEC",
    },
  ],
  estimate,
  storage: {},
  limits: {},
  ...over,
});

function renderPanel() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <RemotePanel />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("RemotePanel", () => {
  beforeEach(() => {
    get.mockReset();
    localStorage.removeItem("mkt:locale");
  });

  it("says when Cloudflare Stream is not set up", async () => {
    get.mockResolvedValue({ integration: integration({ configured: false, enabled: false }) });
    renderPanel();
    expect(await screen.findByTestId("remote-status")).toHaveTextContent(/not set up/);
    expect(screen.getByRole("link", { name: /set it up/i })).toHaveAttribute(
      "href",
      "/admin/extensions#cloudflare_stream"
    );
  });

  it("says when remote viewing is switched off", async () => {
    get.mockResolvedValue({ integration: integration({ enabled: false }), remote: remote() });
    renderPanel();
    expect(await screen.findByTestId("remote-status")).toHaveTextContent(/switched off/);
  });

  it("shows inputs, pushes with both viewer counts, sessions, the month and events", async () => {
    get.mockResolvedValue({ integration: integration(), remote: remote() });
    renderPanel();
    expect(await screen.findByTestId("remote-status")).toHaveTextContent(/ready/);
    expect(screen.getByTestId("remote-month")).toHaveTextContent("150 minutes");
    expect(screen.getByTestId("remote-month")).toHaveTextContent("$5.15");
    const pushes = screen.getByTestId("remote-pushes");
    const live = within(pushes).getByText("105 Quran").closest("tr")!;
    expect(live).toHaveTextContent("2 / 3");
    const failed = within(pushes).getByText("106 Kids").closest("tr")!;
    expect(failed).toHaveTextContent("ERR_UNSUPPORTED_VIDEO_CODEC");
    expect(within(screen.getByTestId("remote-sessions")).getByText("amina")).toBeInTheDocument();
    expect(
      within(screen.getByTestId("remote-sessions")).getByText("Safari on iOS")
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("remote-inputs")).getByText("broadcasting")
    ).toBeInTheDocument();
    const events = screen.getByTestId("remote-events");
    expect(events).toHaveTextContent("tv_push_error");
    expect(events).toHaveTextContent("ERR_UNSUPPORTED_VIDEO_CODEC");
  });

  it("shows the 80 % budget notice (32.10 AC4)", async () => {
    get.mockResolvedValue({
      integration: integration(),
      remote: remote({
        budget_notice: { month: "2026-10", level: 80, total_usd: 8.1, budget_usd: 10, action: "block_new" },
      }),
    });
    renderPanel();
    const n = await screen.findByTestId("remote-budget-notice");
    expect(n).toHaveTextContent("reached 80% of the monthly budget ($8.10 of $10.00)");
    expect(n).not.toHaveTextContent(/refused/);
  });

  it("shows the 100 % budget notice and what it does (32.10 AC4)", async () => {
    get.mockResolvedValue({
      integration: integration(),
      remote: remote({
        budget_notice: { month: "2026-10", level: 100, total_usd: 10.2, budget_usd: 10, action: "block_new" },
      }),
    });
    renderPanel();
    const n = await screen.findByTestId("remote-budget-notice");
    expect(n).toHaveTextContent("reached the monthly budget ($10.20 of $10.00)");
    expect(n).toHaveTextContent(/New remote tunes are refused/);
  });

  it("shows no budget notice below 80 %", async () => {
    get.mockResolvedValue({ integration: integration(), remote: remote() });
    renderPanel();
    await screen.findByTestId("remote-month");
    expect(screen.queryByTestId("remote-budget-notice")).toBeNull();
  });

  it("shows — when Cloudflare hides the viewer count (EC1)", async () => {
    const r = remote();
    (r.pushes as Array<Record<string, unknown>>)[0].cf_viewers = null;
    get.mockResolvedValue({ integration: integration(), remote: r });
    renderPanel();
    const pushes = await screen.findByTestId("remote-pushes");
    const row = within(pushes).getByText("105 Quran").closest("tr")!;
    expect(row).toHaveTextContent("2 / —");
  });

  it("shows why remote viewing cannot serve viewers", async () => {
    get.mockResolvedValue({
      integration: integration(),
      remote: remote({
        ready: false,
        reason: "no signing key: run Test connection",
        unavailable: "Cloudflare: storage full",
      }),
    });
    renderPanel();
    expect(await screen.findByTestId("remote-status")).toHaveTextContent(/no signing key/);
    expect(screen.getByRole("alert")).toHaveTextContent(/storage full/);
  });

  it("pages long session lists (EC2)", async () => {
    const many = Array.from({ length: REMOTE_PAGE + 5 }, (_, i) => ({
      id: `s${i}`,
      user: `user${i}`,
      device: "Chrome",
      channel_id: "c1",
      started_at: "2026-10-19T10:01:00Z",
      last_beat_at: "2026-10-19T10:05:00Z",
    }));
    get.mockResolvedValue({ integration: integration(), remote: remote({ session_list: many }) });
    renderPanel();
    const table = await screen.findByTestId("remote-sessions");
    expect(table.querySelectorAll("tbody tr")).toHaveLength(REMOTE_PAGE);
    screen.getByRole("button", { name: /show more/i }).click();
    expect(await screen.findByText(`user${REMOTE_PAGE + 4}`)).toBeInTheDocument();
  });

  it("reads in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue({ integration: integration(), remote: remote() });
    renderPanel();
    expect(
      await screen.findByText("المشاهدة عن بُعد", { selector: "h2 *, h2" })
    ).toBeInTheDocument();
    expect(screen.getByTestId("remote-status")).toHaveTextContent("جاهزة");
  });
});
