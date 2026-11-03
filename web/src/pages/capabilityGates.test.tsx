// Story 51.11 — the pages' gates: a viewer loses what is off on this home, an
// admin reads that it is off. Search by meaning off: no mode to pick and, for
// an admin, the words-only line; YouTube off: no YouTube leg; transcripts or
// chapters off: no tab for a viewer, the note in it for an admin.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { post, get } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get } };
});
vi.mock("../lib/ws", () => ({ subscribe: () => () => {} }));
vi.mock("../components/MediaContextCard", () => ({
  MediaContextCard: () => <div data-testid="context-card" />,
}));
vi.mock("../components/EnrichmentPanel", () => ({
  EnrichmentPanel: () => <div data-testid="enrichment-panel" />,
}));

import { Search } from "./Search";
import { VideoDetail } from "./VideoDetail";
import { I18nProvider } from "../lib/i18n";
import { CapabilitiesClient, CapabilitiesProvider } from "../lib/capabilities";
import { TestAuth, adminUser, memberUser } from "../test/pageHarness";
import type { User } from "../lib/auth";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

function client(off: string[], admin: boolean) {
  const features: Record<string, string> = {};
  for (const f of off) features[f] = "off";
  const body: Record<string, unknown> = { etag: "c-1-0", features };
  if (admin) {
    const blocks: Record<string, unknown> = {};
    for (const f of off) blocks[f] = { components: ["x"], reason: "switched_off" };
    body.admin = { manage_components: true, off: blocks };
  }
  return new CapabilitiesClient(async () => ({ status: "ok", body, etag: null }), Date.now, true);
}

function renderAt(ui: React.ReactNode, user: User, c: CapabilitiesClient, path = "/") {
  return render(
    <I18nProvider>
      <ToastProvider>
        <TestAuth user={user}>
          <CapabilitiesProvider client={c}>
            <MemoryRouter initialEntries={[path]}>{ui}</MemoryRouter>
          </CapabilitiesProvider>
        </TestAuth>
      </ToastProvider>
    </I18nProvider>
  );
}

function serveSearch() {
  get.mockImplementation((url: string) => {
    if (url === "/api/search/youtube/status") return Promise.resolve({ available: true });
    return Promise.resolve({ items: [], suggestions: [] });
  });
  post.mockImplementation((url: string) =>
    url === "/api/search"
      ? Promise.resolve({
          search_id: "",
          page_size: 20,
          passages_page_size: 20,
          videos: [{ video_id: "vid-1", title: "kahf lesson one" }],
          videos_total: 1,
          transcript_videos: [],
          transcript_total: 0,
        })
      : Promise.resolve({})
  );
}

describe("Search with search by meaning off", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    serveSearch();
  });

  // T13 (web)
  it("an admin reads the words-only line and has no mode to pick", async () => {
    renderAt(<Search />, adminUser, client(["semantic_search"], true));
    expect(await screen.findByText(en["capabilities.wordsOnly"])).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: en["search.mode"] })).not.toBeInTheDocument();
  });

  it("a viewer has no mode to pick and no line", async () => {
    renderAt(<Search />, memberUser, client(["semantic_search"], false));
    await waitFor(() =>
      expect(screen.queryByRole("combobox", { name: en["search.mode"] })).not.toBeInTheDocument()
    );
    expect(screen.queryByText(en["capabilities.wordsOnly"])).not.toBeInTheDocument();
  });

  it("the words-only line in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    try {
      renderAt(<Search />, adminUser, client(["semantic_search"], true));
      expect(await screen.findByText(ar["capabilities.wordsOnly"])).toBeInTheDocument();
    } finally {
      localStorage.removeItem("mkt:locale");
    }
  });

  it("with everything on the mode stays", async () => {
    renderAt(<Search />, adminUser, client([], true));
    expect(await screen.findByRole("combobox", { name: en["search.mode"] })).toBeInTheDocument();
  });
});

describe("Search with YouTube off", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    serveSearch();
  });

  it("a viewer has no YouTube toggle and the YouTube search is never asked", async () => {
    renderAt(<Search />, memberUser, client(["youtube"], false));
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/search/youtube/status"));
    await waitFor(() =>
      expect(screen.queryByLabelText(en["search.youtube.toggle"])).not.toBeInTheDocument()
    );
  });

  it("an admin reads that YouTube is off once a search ran", async () => {
    renderAt(<Search />, adminUser, client(["youtube"], true));
    const user = userEvent.setup();
    await user.type(await screen.findByRole("combobox", { name: en["nav.search"] }), "kahf");
    await user.click(screen.getAllByRole("button", { name: en["nav.search"] })[0]);
    expect(await screen.findByText("YouTube is off on this home.")).toBeInTheDocument();
    expect(get.mock.calls.some(([u]) => String(u).startsWith("/api/search/youtube?"))).toBe(false);
  });
});

describe("VideoDetail with transcripts and chapters off", () => {
  beforeEach(() => {
    get.mockReset();
    get.mockImplementation((url: string) =>
      url === "/api/videos/v1"
        ? Promise.resolve({ id: "v1", filename: "a.mp4", title: "Clip", state: "ready" })
        : Promise.resolve({ items: [] })
    );
  });

  const page = (
    <Routes>
      <Route path="/videos/:videoId" element={<VideoDetail />} />
    </Routes>
  );

  it("a viewer has no Transcript or Chapters tab", async () => {
    renderAt(page, memberUser, client(["transcripts", "chapters"], false), "/videos/v1");
    await screen.findByRole("tab", { name: en["video.tab.files"] });
    await waitFor(() =>
      expect(
        screen.queryByRole("tab", { name: en["video.tab.transcript"] })
      ).not.toBeInTheDocument()
    );
    expect(screen.queryByRole("tab", { name: en["video.tab.chapters"] })).not.toBeInTheDocument();
  });

  it("an admin's Transcript tab says it is off", async () => {
    renderAt(page, adminUser, client(["transcripts"], true), "/videos/v1?tab=transcript");
    expect(await screen.findByText("Transcripts is off on this home.")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: en["video.tab.chapters"] })).toBeInTheDocument();
  });
});
