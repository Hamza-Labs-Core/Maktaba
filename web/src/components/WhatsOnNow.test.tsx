// Story 27.9 — "What's On Now" home rail tests. The rail is a read path
// over /api/channels/now; it renders current + next per channel, hides
// itself when there are no accessible channels, and Tune In navigates to
// the live player.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { WhatsOnNow } from "./WhatsOnNow";
import { I18nProvider } from "../lib/i18n";

const NOW = Date.now();
const iso = (ms: number) => new Date(NOW + ms).toISOString();

const payload = {
  server_time: new Date(NOW).toISOString(),
  items: [
    {
      channel: { id: "c1", number: 1, name: "Movies", mode: "shuffle", enabled: true, sort_order: 0 },
      current: {
        channel_id: "c1",
        seq: 0,
        kind: "program",
        video_id: "v1",
        title: "Now Airing",
        start_at: iso(-5 * 60_000),
        end_at: iso(25 * 60_000),
      },
      next: {
        channel_id: "c1",
        seq: 1,
        kind: "program",
        title: "Up Next Show",
        start_at: iso(25 * 60_000),
        end_at: iso(85 * 60_000),
      },
    },
  ],
};

function renderRail() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <I18nProvider>
        <Routes>
          <Route path="/" element={<WhatsOnNow />} />
          <Route path="/live/:number" element={<div>LIVE PLAYER</div>} />
        </Routes>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("WhatsOnNow rail", () => {
  beforeEach(() => get.mockReset());

  // TC1 — current + next per channel.
  it("renders the current and next program", async () => {
    get.mockResolvedValue(payload);
    renderRail();
    expect(await screen.findByText("Now Airing")).toBeInTheDocument();
    expect(screen.getByText(/Up Next Show/)).toBeInTheDocument();
  });

  // TC6 — hidden entirely when there are no accessible channels.
  it("renders nothing when there are no channels", async () => {
    get.mockResolvedValue({ server_time: new Date(NOW).toISOString(), items: [] });
    const { container } = renderRail();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(container.querySelector(".mkt-won")).toBeNull();
  });

  // TC2 — Tune In navigates to the live player.
  it("navigates to the live player on tune", async () => {
    get.mockResolvedValue(payload);
    renderRail();
    const card = await screen.findByRole("button", { name: /tune in to movies/i });
    card.click();
    expect(await screen.findByText("LIVE PLAYER")).toBeInTheDocument();
  });
});
