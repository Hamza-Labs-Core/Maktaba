// Story 29.4 — the full watch history and the activity timeline read the
// real /api/me/history and /api/me/activity contracts.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent, within } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

const { get, del } = vi.hoisted(() => ({ get: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, delete: del } };
});

import { WatchHistory, HISTORY_PAGE } from "./WatchHistory";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const hist = (i: number) => ({
  video_id: `v${i}`,
  title: `Video ${i}`,
  duration_sec: 600,
  times_watched: 1,
  total_watch_sec: 300,
  best_percent: 50,
  last_watched_at: "2026-03-01T12:00:00Z",
  position_sec: 300,
  completed: false,
});

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <I18nProvider>
        <ToastProvider>
          <Routes>
            <Route path="/account/history" element={<WatchHistory />} />
          </Routes>
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("WatchHistory", () => {
  beforeEach(() => {
    get.mockReset();
    del.mockReset();
  });

  it("pages the full history with Load more and removes a row", async () => {
    get.mockImplementation((url: string) => {
      if (url.startsWith("/api/me/history")) {
        const offset = Number(new URL(url, "http://x").searchParams.get("offset") ?? 0);
        const items =
          offset === 0 ? Array.from({ length: HISTORY_PAGE }, (_, i) => hist(i)) : [hist(999)];
        return Promise.resolve({ items, limit: HISTORY_PAGE, offset });
      }
      return Promise.resolve({ items: [] });
    });
    del.mockResolvedValue(undefined);
    renderAt("/account/history");
    expect(await screen.findByText("Video 0")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(`/api/me/history?limit=${HISTORY_PAGE}&offset=0`);

    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Video 999")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(
      `/api/me/history?limit=${HISTORY_PAGE}&offset=${HISTORY_PAGE}`
    );
    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();

    const row = screen.getByText("Video 0").closest("tr")!;
    fireEvent.click(within(row).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/me/history/v0"));
    await waitFor(() => expect(screen.queryByText("Video 0")).not.toBeInTheDocument());
  });

  it("shows the activity timeline from ?tab=activity and filters by kind", async () => {
    get.mockImplementation((url: string) => {
      if (url.startsWith("/api/me/activity")) {
        return Promise.resolve({
          items: [
            {
              kind: "watched",
              at: "2026-03-02T09:30:00Z",
              meta: { video_id: "v7", title: "Tafsir 7", percent_complete: 80 },
            },
            { kind: "searched", at: "2026-03-01T08:00:00Z", meta: { query: "fiqh" } },
          ],
          limit: 50,
          offset: 0,
        });
      }
      return Promise.resolve({ items: [] });
    });
    renderAt("/account/history?tab=activity");
    const link = await screen.findByRole("link", { name: "Tafsir 7" });
    expect(link).toHaveAttribute("href", "/videos/v7");
    expect(screen.getByRole("link", { name: "fiqh" })).toHaveAttribute("href", "/search?q=fiqh");
    expect(screen.getByText(/80%/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/me/activity?limit=50&offset=0");

    fireEvent.click(screen.getByRole("button", { name: "Searched" }));
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith("/api/me/activity?limit=50&offset=0&types=searched")
    );
  });
});
