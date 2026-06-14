// Story 26.10 — SeriesBrowser renders the cross-library grid and, per
// AC test_rtl_render, an Arabic series name carries dir="auto" so it
// renders right-to-left.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { SeriesBrowser } from "./SeriesBrowser";
import { I18nProvider } from "../../lib/i18n";

function renderBrowser() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <SeriesBrowser />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("SeriesBrowser", () => {
  beforeEach(() => get.mockReset());

  it("renders series cards from the cross-library list", async () => {
    get.mockResolvedValue({
      items: [
        {
          id: "s1",
          name: "Breaking Bad",
          numbering: "season",
          season_count: 5,
          episode_count: 62,
          watched_count: 10,
          in_progress: 1,
          year: 2008,
        },
      ],
    });
    renderBrowser();
    await waitFor(() => expect(screen.getByText("Breaking Bad")).toBeInTheDocument());
  });

  it("marks an Arabic series name dir=auto for RTL", async () => {
    get.mockResolvedValue({
      items: [
        { id: "s2", name: "باب الحارة", numbering: "season", season_count: 11, episode_count: 400, watched_count: 0, in_progress: 0 },
      ],
    });
    renderBrowser();
    const name = await screen.findByText("باب الحارة");
    expect(name).toHaveAttribute("dir", "auto");
  });

  it("shows an empty state when there are no series", async () => {
    get.mockResolvedValue({ items: [] });
    renderBrowser();
    await waitFor(() => expect(screen.getByText(/No series detected yet/)).toBeInTheDocument());
  });
});
