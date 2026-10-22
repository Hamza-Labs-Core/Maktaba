// Story 26.8 — "From YouTube" on the Search page: shown only when the
// server can run yt-dlp, fetched after the local results, and each
// result has an Import button that stages its metadata onto a local video
// picked in a dialog, then refetches the results with a toast.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { post, get } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, post, get } };
});

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ user: { id: "a1", username: "admin", is_admin: true } }),
}));

import { Search } from "./Search";
import { I18nProvider } from "../lib/i18n";

const ytItem = {
  youtube_id: "AvctFaYGuR0",
  title: "Surah Al Kahf | Lesson 1",
  channel: "Hisham",
  description_snippet: "A lesson",
  thumbnail: "https://i.ytimg.com/vi/AvctFaYGuR0/hq720.jpg",
  published_at: "2024-01-15",
  duration_sec: 633,
  view_count: 1200,
  match: { state: "importable" },
};

function mockServer(opts: { available: boolean; ytItems?: unknown[]; imported?: boolean }) {
  let importedYet = false;
  get.mockImplementation((url: string) => {
    if (url === "/api/search/youtube/status") return Promise.resolve({ available: opts.available });
    if (url.startsWith("/api/search/youtube?")) {
      const items = (opts.ytItems ?? [ytItem]).map((it) =>
        importedYet ? { ...(it as object), match: { state: "imported", video_id: "vid-1" } } : it
      );
      return Promise.resolve({ items });
    }
    return Promise.resolve({ items: [], suggestions: [] });
  });
  post.mockImplementation((url: string, body: { q?: string }) => {
    if (url === "/api/search") {
      // The saved-search shape; an admin-token search has no sid.
      return Promise.resolve({
        search_id: "",
        page_size: 20,
        passages_page_size: 20,
        videos:
          body.q === "other"
            ? [{ video_id: "vid-2", title: "Another lesson" }]
            : [{ video_id: "vid-1", title: "kahf lesson one" }],
        videos_total: 1,
        transcript_videos: [],
        transcript_total: 0,
      });
    }
    if (url.includes("/import-youtube")) {
      importedYet = true;
      return Promise.resolve({ staged: true });
    }
    return Promise.resolve({});
  });
}

function renderSearch() {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <ToastProvider>
          <Search />
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

async function searchFor(term: string, withYouTube: boolean) {
  const user = userEvent.setup();
  if (withYouTube) {
    await user.click(await screen.findByLabelText("Also search YouTube"));
  }
  await user.type(screen.getByRole("combobox", { name: "Search" }), term);
  await user.click(screen.getAllByRole("button", { name: "Search" })[0]);
  return user;
}

describe("Search — From YouTube", () => {
  beforeEach(() => {
    post.mockReset();
    get.mockReset();
  });

  it("hides the toggle and never calls YouTube search when yt-dlp is unavailable", async () => {
    mockServer({ available: false });
    renderSearch();
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/search/youtube/status"));
    expect(screen.queryByLabelText("Also search YouTube")).not.toBeInTheDocument();
    await searchFor("kahf", false);
    await screen.findByText("kahf lesson one");
    expect(screen.queryByText("From YouTube")).not.toBeInTheDocument();
    expect(get.mock.calls.some(([u]) => String(u).startsWith("/api/search/youtube?"))).toBe(false);
  });

  it("fetches YouTube after the local search and shows the results", async () => {
    mockServer({ available: true });
    renderSearch();
    await searchFor("kahf", true);
    await screen.findByText("kahf lesson one");
    // the local search no longer waits on YouTube
    expect(post.mock.calls[0][1]).not.toHaveProperty("include");
    await screen.findByText("Surah Al Kahf | Lesson 1");
    expect(get).toHaveBeenCalledWith("/api/search/youtube?q=kahf");
    expect(screen.getByText(/Hisham · 10:33 · 2024-01-15/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Surah Al Kahf/ })).toHaveAttribute(
      "rel",
      "noopener noreferrer"
    );
  });

  it("imports a result onto a picked video, toasts and refetches", async () => {
    mockServer({ available: true });
    renderSearch();
    const user = await searchFor("kahf", true);
    await screen.findByText("Surah Al Kahf | Lesson 1");
    const promptSpy = vi.spyOn(window, "prompt").mockReturnValue(null);

    await user.click(screen.getByRole("button", { name: "Import" }));
    const dialog = await screen.findByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "Import" });
    expect(confirm).toBeDisabled();
    // the current search's title matches are offered first
    await user.click(within(dialog).getByLabelText(/kahf lesson one/));
    expect(confirm).toBeEnabled();

    const searchesBefore = post.mock.calls.filter(([u]) => u === "/api/search").length;
    await user.click(confirm);

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/videos/vid-1/import-youtube", {
        youtube_id: "AvctFaYGuR0",
        title: "Surah Al Kahf | Lesson 1",
        channel: "Hisham",
        description: "A lesson",
        thumbnail: "https://i.ytimg.com/vi/AvctFaYGuR0/hq720.jpg",
        published_at: "2024-01-15",
        duration_sec: 633,
      })
    );
    expect(await screen.findByText(/Metadata staged for “kahf lesson one”/)).toBeInTheDocument();
    // results refetched: a new local search and the result now says imported
    await waitFor(() =>
      expect(post.mock.calls.filter(([u]) => u === "/api/search").length).toBe(searchesBefore + 1)
    );
    expect(await screen.findByText("Imported →")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(promptSpy).not.toHaveBeenCalled();
    promptSpy.mockRestore();
  });

  it("can look up another video by title in the import dialog", async () => {
    mockServer({ available: true });
    renderSearch();
    const user = await searchFor("kahf", true);
    await screen.findByText("Surah Al Kahf | Lesson 1");
    await user.click(screen.getByRole("button", { name: "Import" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByRole("searchbox"), "other");
    await user.click(within(dialog).getByRole("button", { name: "Search" }));
    expect(post).toHaveBeenLastCalledWith("/api/search", {
      q: "other",
      mode: "fts",
      view: "list",
      save: false,
    });
    await user.click(await within(dialog).findByLabelText(/Another lesson/));
    await user.click(within(dialog).getByRole("button", { name: "Import" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        "/api/videos/vid-2/import-youtube",
        expect.objectContaining({ youtube_id: "AvctFaYGuR0" })
      )
    );
  });

  it("shows a note, not an empty box, when YouTube fails", async () => {
    mockServer({ available: true });
    get.mockImplementation((url: string) => {
      if (url === "/api/search/youtube/status") return Promise.resolve({ available: true });
      if (url.startsWith("/api/search/youtube?")) return Promise.reject(new Error("down"));
      return Promise.resolve({ items: [] });
    });
    renderSearch();
    await searchFor("kahf", true);
    expect(await screen.findByText("YouTube is unavailable right now.")).toBeInTheDocument();
  });
});
