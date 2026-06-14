// Story 27.6 — EPG grid UI tests. The guide is a read path over
// /api/channels/guide; cells are sized proportional to duration, the
// airing cell tunes, and the "What's On Now" toggle swaps to the compact
// list. api is mocked at the wrapper layer so channels.ts runs for real.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { Guide } from "./Guide";
import { I18nProvider } from "../../lib/i18n";

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
        programs: [
          {
            channel_id: "c1",
            seq: 0,
            kind: "program",
            video_id: "v-now",
            title: "Now Playing",
            start_at: iso(-10 * 60_000),
            end_at: iso(20 * 60_000),
          },
          {
            channel_id: "c1",
            seq: 1,
            kind: "program",
            video_id: "v-later",
            title: "Later Show",
            start_at: iso(20 * 60_000),
            end_at: iso(140 * 60_000),
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
        <Routes>
          <Route path="/guide" element={<Guide />} />
          <Route path="/live/:number" element={<div>LIVE PLAYER</div>} />
          <Route path="/admin/channels" element={<div>ADMIN</div>} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Guide (EPG grid)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  // TC1 — proportional cells: a 30-min vs 120-min program render 1:4.
  it("sizes cells proportional to duration", async () => {
    get.mockResolvedValue(guidePayload());
    renderGuide();
    const cells = await screen.findAllByTestId("guide-cell");
    expect(cells.length).toBe(2);
    const w = (el: HTMLElement) => parseFloat(el.style.width);
    const widths = cells.map(w).sort((a, b) => a - b);
    expect(widths[1] / widths[0]).toBeCloseTo(4, 1);
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
