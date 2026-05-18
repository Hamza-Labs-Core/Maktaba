// R4.1 — Search must speak the real server contract:
//   POST /api/search  { q, mode, limit }
//   -> { hits: [{ segment_id, video_id, start_sec, end_sec, snippet, score }], total, ... }
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

const { post, get, del } = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get, delete: del } };
});

import { Search } from "./Search";
import { I18nProvider } from "../lib/i18n";

function renderSearch() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <Search />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("Search", () => {
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
    del.mockReset();
    // Saved-searches sidebar fetch on mount.
    get.mockResolvedValue({ items: [], suggestions: [] });
  });

  it("POSTs /api/search with { q, mode, limit } and renders server hits", async () => {
    post.mockResolvedValue({
      hits: [
        {
          segment_id: 42,
          video_id: "vid-1",
          start_sec: 12.5,
          end_sec: 18.0,
          snippet: "the <mark>quick</mark> brown fox",
          score: 0.9,
        },
      ],
      total: 1,
      took_ms: { fts: 3, semantic: 0, fusion: 1 },
      mode: "hybrid",
      filters: {},
    });

    renderSearch();
    const user = userEvent.setup();
    await user.type(screen.getByRole("combobox", { name: /search/i }), "quick");
    await user.click(screen.getByRole("button", { name: /^search$/i }));

    await waitFor(() => {
      expect(post).toHaveBeenCalledTimes(1);
    });
    const [path, body] = post.mock.calls[0];
    expect(path).toBe("/api/search");
    expect(body).toMatchObject({ q: "quick" });
    expect(body).toHaveProperty("mode");
    expect(body).toHaveProperty("limit");

    // Hit content from the server `snippet` is rendered.
    await waitFor(() => {
      expect(screen.getByText(/brown fox/)).toBeInTheDocument();
    });
  });

  it("renders the empty state when the server returns no hits", async () => {
    post.mockResolvedValue({
      hits: [],
      total: 0,
      took_ms: { fts: 1, semantic: 0, fusion: 0 },
      mode: "hybrid",
      filters: {},
    });

    renderSearch();
    const user = userEvent.setup();
    await user.type(screen.getByRole("combobox", { name: /search/i }), "nothing");
    await user.click(screen.getByRole("button", { name: /^search$/i }));

    await waitFor(() => {
      expect(screen.getByText(/no results/i)).toBeInTheDocument();
    });
  });
});
