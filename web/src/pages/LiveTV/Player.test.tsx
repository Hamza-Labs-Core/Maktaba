// Story 27.7 — live channel player tests. The player tunes via
// POST /api/channels/{id}/tune, shows a LIVE indicator + tune banner, a
// mini-guide overlay, and resolves an unknown channel number to a
// not-found state.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import { Player } from "./Player";
import { I18nProvider } from "../../lib/i18n";

const NOW = Date.now();
const iso = (ms: number) => new Date(NOW + ms).toISOString();

const lineup = {
  items: [
    { id: "c1", number: 1, name: "Movies", mode: "shuffle", enabled: true, sort_order: 0 },
    { id: "c2", number: 2, name: "Kids", mode: "shuffle", enabled: true, sort_order: 1 },
  ],
};

const nowPayload = {
  server_time: new Date(NOW).toISOString(),
  items: [
    {
      channel: lineup.items[0],
      current: {
        channel_id: "c1",
        seq: 0,
        kind: "program",
        video_id: "v1",
        title: "Now on One",
        start_at: iso(-5 * 60_000),
        end_at: iso(25 * 60_000),
      },
      next: {
        channel_id: "c1",
        seq: 1,
        kind: "program",
        title: "Next on One",
        start_at: iso(25 * 60_000),
        end_at: iso(85 * 60_000),
      },
    },
    { channel: lineup.items[1], current: null, next: null },
  ],
};

function mockGet() {
  get.mockImplementation((url: string) =>
    url.includes("/now") ? Promise.resolve(nowPayload) : Promise.resolve(lineup)
  );
}

function renderPlayer(num = "1") {
  return render(
    <MemoryRouter initialEntries={[`/live/${num}`]}>
      <I18nProvider>
        <Routes>
          <Route path="/live/:number" element={<Player />} />
          <Route path="/guide" element={<div>GUIDE</div>} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Player (live channel)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    del.mockReset();
    post.mockResolvedValue({ session_id: "s1", manifest_url: "http://x/m.m3u8", channel_id: "c1" });
    del.mockResolvedValue(undefined);
  });

  // TC1 — distinct live mode: a LIVE indicator + a video element.
  it("shows a LIVE indicator and plays the tuned manifest", async () => {
    mockGet();
    renderPlayer("1");
    expect(await screen.findByText(/LIVE/)).toBeInTheDocument();
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/channels/c1/tune")
    );
    const video = (await screen.findByTestId("live-video")) as HTMLVideoElement;
    expect(video.getAttribute("src")).toBe("http://x/m.m3u8");
  });

  // TC4 — the tune banner shows the current + next program.
  it("renders a tune banner with current and next program", async () => {
    mockGet();
    renderPlayer("1");
    const banner = await screen.findByTestId("tune-banner");
    expect(banner).toHaveTextContent("Now on One");
    expect(banner).toHaveTextContent("Next on One");
  });

  // TC5 — the mini-guide overlay lists channels over the playing channel.
  it("opens a mini-guide listing channels", async () => {
    mockGet();
    renderPlayer("1");
    const guideBtn = await screen.findByRole("button", { name: /mini-guide/i });
    guideBtn.click();
    const mg = await screen.findByTestId("mini-guide");
    expect(mg).toHaveTextContent("Movies");
    expect(mg).toHaveTextContent("Kids");
  });

  // EC6 — an unknown channel number resolves to a not-found state.
  it("shows a not-found state for an unknown channel number", async () => {
    mockGet();
    renderPlayer("9");
    expect(await screen.findByText(/channel not found/i)).toBeInTheDocument();
  });
});
