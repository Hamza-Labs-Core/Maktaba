// Story 27.6 — EPG grid UI tests. The guide is a read path over
// /api/channels/guide; cells are sized proportional to duration, the
// airing cell tunes, and the "What's On Now" toggle swaps to the compact
// list. api is mocked at the wrapper layer so channels.ts runs for real.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { Guide } from "./Guide";
import { I18nProvider } from "../../lib/i18n";
import { TestAuth, adminUser } from "../../test/pageHarness";

const NOW = Date.now();
const iso = (msFromNow: number) => new Date(NOW + msFromNow).toISOString();

// One channel: a 30-min airing program followed by a 120-min future one.
function guidePayload() {
  return {
    server_time: new Date(NOW).toISOString(),
    channels: [
      {
        channel: {
          id: "c1",
          number: 1,
          name: "Movies",
          category: "movies",
          mode: "shuffle",
          enabled: true,
          sort_order: 0,
        },
        blocks: [
          {
            channel_id: "c1",
            seq: 0,
            kind: "program",
            video_id: "v-now",
            title: "Now Playing",
            start: iso(-10 * 60_000),
            stop: iso(20 * 60_000),
          },
          {
            channel_id: "c1",
            seq: 1,
            kind: "program",
            video_id: "v-later",
            title: "Later Show",
            start: iso(20 * 60_000),
            stop: iso(140 * 60_000),
          },
        ],
      },
    ],
  };
}

function renderGuide() {
  return render(
    <MemoryRouter initialEntries={["/guide"]}>
      <I18nProvider>
        {/* an admin: the empty state's "Create a channel" CTA is admin-only */}
        <TestAuth user={adminUser}>
          <Routes>
            <Route path="/guide" element={<Guide />} />
            <Route path="/live/:number" element={<div>LIVE PLAYER</div>} />
            <Route path="/admin/channels" element={<div>ADMIN</div>} />
          </Routes>
        </TestAuth>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Guide (EPG grid)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  // TC1 — proportional cells: a 30-min vs 120-min program render 1:4. The
  // window is two hours, so the 120-min one is the whole window (on its own
  // row) and neither is cut; the clock is frozen so the window starts exactly
  // where the long programme does.
  it("sizes cells proportional to duration", async () => {
    vi.useFakeTimers({ toFake: ["Date"], now: NOW });
    try {
      const payload = guidePayload();
      const [short, long] = payload.channels[0].blocks;
      short.start = iso(-30 * 60_000);
      short.stop = iso(0);
      long.start = iso(-30 * 60_000);
      long.stop = iso(90 * 60_000);
      payload.channels.push({
        ...payload.channels[0],
        channel: { ...payload.channels[0].channel, id: "c2", number: 2, name: "Films" },
        blocks: [long],
      });
      payload.channels[0].blocks = [short];
      get.mockResolvedValue(payload);
      renderGuide();
      const cells = await screen.findAllByTestId("guide-cell");
      expect(cells.length).toBe(2);
      for (const c of cells) {
        expect(c).not.toHaveClass("mkt-guide__cell--cut-start");
        expect(c).not.toHaveClass("mkt-guide__cell--cut-end");
      }
      const w = (el: HTMLElement) => parseFloat(el.style.width);
      const widths = cells.map(w).sort((a, b) => a - b);
      expect(widths[1] / widths[0]).toBeCloseTo(4, 5);
      expect(widths[1]).toBeCloseTo(100, 5); // two hours fill the guide's width
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows channel logos and hides a logo that fails to load", async () => {
    const payload = guidePayload();
    (payload.channels[0].channel as Record<string, unknown>).logo_url = "/api/channels/c1/logo";
    get.mockResolvedValue(payload);
    renderGuide();
    const logo = await screen.findByTestId("channel-logo");
    expect(logo.getAttribute("src")).toBe("/api/channels/c1/logo");
    fireEvent.error(logo);
    await waitFor(() => expect(screen.queryByTestId("channel-logo")).toBeNull());
  });

  // TC2 — the now-line is present.
  it("renders a now-line", async () => {
    get.mockResolvedValue(guidePayload());
    renderGuide();
    expect(await screen.findByTestId("now-line")).toBeInTheDocument();
  });

  // TC3 — clicking the airing cell navigates to the live player.
  it("tunes when the airing cell is clicked", async () => {
    get.mockResolvedValue(guidePayload());
    renderGuide();
    const cells = await screen.findAllByTestId("guide-cell");
    const airing = cells.find((c) => c.className.includes("airing"))!;
    airing.click();
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });

  // The channel's name, number and logo tune too, not only the programme.
  it("tunes when the channel name is clicked", async () => {
    get.mockResolvedValue(guidePayload());
    renderGuide();
    const grid = await screen.findByTestId("guide-grid");
    const heads = grid.querySelectorAll<HTMLAnchorElement>("a.mkt-guide__chanhead");
    expect(heads.length).toBeGreaterThan(0);
    expect(heads[0].getAttribute("href")).toMatch(/^\/live\/\d+$/);
    heads[0].click();
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });

  // TC4 — clicking a future cell opens details, does not tune.
  it("opens details for a future cell without tuning", async () => {
    get.mockResolvedValue(guidePayload());
    renderGuide();
    const cells = await screen.findAllByTestId("guide-cell");
    const future = cells.find((c) => !c.className.includes("airing"))!;
    future.click();
    await waitFor(() => expect(screen.getByText("Later Show")).toBeInTheDocument());
    expect(screen.queryByText("LIVE PLAYER")).not.toBeInTheDocument();
  });

  // TC6 — the "What's On Now" toggle swaps to the compact list.
  it("toggles the What's On Now compact view", async () => {
    get.mockResolvedValue(guidePayload());
    renderGuide();
    const toggle = await screen.findByRole("button", { name: /what's on now/i });
    toggle.click();
    const compact = await screen.findByTestId("whats-on-now");
    expect(within(compact).getByText("Now Playing")).toBeInTheDocument();
  });

  // TC9 — empty lineup shows the empty state + create CTA.
  it("shows an empty state with a create CTA when there are no channels", async () => {
    get.mockResolvedValue({ server_time: new Date(NOW).toISOString(), channels: [] });
    renderGuide();
    expect(await screen.findByText(/no channels yet/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /create a channel/i })).toBeInTheDocument();
  });
});
