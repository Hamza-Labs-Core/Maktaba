// Epic 58 story 58.08 T02 — the big screen gains Epic 56's TV screens and its
// remote rules (§6.4, §6.5), in English and Arabic:
//   - a library's tabs, Left / Right along them (swapped in Arabic), Down into
//     the tab where it was left (focus memory per tab);
//   - Back in three steps: the tab's top, the tab row, out of the library;
//   - Play/Pause on a focused card plays it; a long OK or the menu key opens
//     its actions, a short OK presses it;
//   - Play and Shuffle play the Library tab's view with its sort and filters;
//   - the letter rail (A…Z then ا…ي, or ا…ي then A…Z in Arabic);
//   - collections and playlists with their badges, Play and Shuffle;
//   - a series' Mark watched and season actions; the player's Up next in the
//     last 30 s and Shuffle again.
// The view's own rules (URL, queries, rail order) are unit-tested at the end.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { TestAuth, memberUser } from "../test/pageHarness";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import BigScreen from "./BigScreen";
import { resetFocusMemory } from "./focus/FocusProvider";
import { resetLibraryMemory } from "./screens/Library";
import { LongPress, LONG_PRESS_MS } from "./cards";
import {
  filterCount,
  headerPlays,
  queueFilters,
  railLetters,
  readView,
  videosQuery,
  writeView,
} from "./libraryView";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

const card = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  title: `Video ${id}`,
  filename: `${id}.mp4`,
  library_id: "L1",
  duration_sec: 600,
  ...extra,
});

const HUB = {
  library: { id: "L1", name: "Tafsir", videos: 40 },
  collection_rows: [],
  rows: {
    continue_watching: [],
    recently_added: [card("v4")],
    recently_watched: [],
    most_watched: [],
    series: [{ id: "S1", name: "Tafsir al-Fatiha", episodes: 3 }],
    years: [],
  },
};

const GRID = {
  items: [
    {
      id: "v6",
      title: "First",
      filename: "v6.mp4",
      duration_sec: 100,
      playback_state: { position_sec: 50, completed: false },
    },
    { id: "v7", title: "Second", filename: "v7.mp4" },
    { id: "v8", title: "Third", filename: "v8.mp4" },
  ],
  next: null,
};

const EPISODES = {
  seasons: [
    {
      season: 1,
      id: "SS1",
      episodes: [
        {
          video_id: "e1",
          season: 1,
          episode: 1,
          title: "Lesson one",
          duration_sec: 1000,
          progress_pct: 100,
          watched: true,
        },
        {
          video_id: "e2",
          season: 1,
          episode: 2,
          title: "Lesson two",
          duration_sec: 1000,
          progress_pct: 0,
          watched: false,
        },
      ],
    },
    {
      season: 2,
      id: "SS2",
      episodes: [
        {
          video_id: "e3",
          season: 2,
          episode: 1,
          title: "Lesson three",
          duration_sec: 1000,
          progress_pct: 0,
          watched: false,
        },
      ],
    },
  ],
  next_episode: {
    video_id: "e2",
    season: 1,
    episode: 2,
    title: "Lesson two",
    duration_sec: 1000,
    progress_pct: 0,
    watched: false,
  },
};

type Table = Record<string, unknown | ((url: string) => unknown)>;

function serve(extra: Table = {}) {
  const table: Table = {
    "/api/home": {
      continue_watching: [],
      recently_added: [card("v2")],
      recently_watched: [],
      pinned: [],
    },
    "/api/channels/now": { server_time: "", channels: [] },
    "/api/libraries/L1/hub": HUB,
    "/api/videos/facets?library=L1": {
      letters: [
        { letter: "B", videos: 1 },
        { letter: "ب", videos: 1 },
        { letter: "A", videos: 2 },
        { letter: "ا", videos: 1 },
      ],
    },
    "/api/videos?": GRID,
    "/api/libraries/L1/collections": {
      items: [
        { kind: "curated", id: "C1", name: "Favourites", videos: 3, poster_video_ids: ["v6"] },
        {
          kind: "youtube",
          id: "C2",
          name: "Channel uploads",
          videos: 9,
          poster_video_ids: [],
          source_kind: "youtube_channel",
        },
        { kind: "topic", id: "G1", name: "Patience", videos: 4, poster_video_ids: ["v7", "v8"] },
      ],
      counts: {},
      total: 3,
      next: null,
    },
    "/api/me/playlists?library=L1": {
      items: [
        {
          id: "P1",
          name: "Evening",
          videos: 2,
          duration_sec: 3600,
          poster_video_ids: ["v6"],
          kind: "smart",
        },
      ],
    },
    "/api/libraries/L1/categories": {
      speakers: [{ value: "Sheikh A", label: "Sheikh A", count: 5 }],
      topics: [],
      languages: [{ value: "ar", label: "Arabic", count: 7 }],
      years: [{ value: "2024", label: "2024", count: 3 }],
    },
    "/api/series/S1": { id: "S1", name: "Tafsir al-Fatiha" },
    "/api/series/S1/episodes": EPISODES,
    "/api/collections/C1": {
      id: "C1",
      library_id: "L1",
      name: "Favourites",
      is_smart: false,
      videos: 2,
      poster_video_ids: ["v6", "v7"],
    },
    "/api/collections/C1/items": {
      items: [
        { video_id: "v6", video: { title: "First", filename: "v6.mp4", duration_sec: 1800 } },
        { video_id: "v7", video: { title: "Second", filename: "v7.mp4", duration_sec: 1800 } },
      ],
    },
    ...extra,
  };
  get.mockImplementation((url: string) => {
    if (url.startsWith("/api/videos?")) {
      const v = table["/api/videos?"];
      return Promise.resolve(typeof v === "function" ? v(url) : v);
    }
    if (url in table) {
      const v = table[url];
      return Promise.resolve(typeof v === "function" ? v(url) : v);
    }
    if (/^\/api\/videos\/[^/?]+$/.test(url))
      return Promise.resolve({ id: url.split("/").pop(), title: "A video" });
    if (/\/chapters$/.test(url)) return Promise.resolve({ items: [] });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

let where = "";
function Where() {
  const l = useLocation();
  where = l.pathname + l.search;
  return null;
}

function renderBig(path: string, history: string[] = []) {
  return render(
    <I18nProvider>
      <TestAuth user={memberUser}>
        <MemoryRouter initialEntries={[...history, path]}>
          <Where />
          <Routes>
            <Route path="/big/*" element={<BigScreen />} />
            <Route path="*" element={<p>desk page</p>} />
          </Routes>
        </MemoryRouter>
      </TestAuth>
    </I18nProvider>
  );
}

const key = (k: string) => fireEvent.keyDown(window, { key: k });
const focused = () => document.activeElement?.getAttribute("data-bs-focus");
const el = (id: string) => document.querySelector<HTMLElement>(`[data-bs-focus="${id}"]`)!;

/** Lays a row of elements out side by side (left to right, or right to left). */
function layRow(els: HTMLElement[], y: number, rtl = false) {
  els.forEach((e, i) => {
    const x = (rtl ? els.length - 1 - i : i) * 200;
    e.getBoundingClientRect = () =>
      ({
        x,
        y,
        left: x,
        top: y,
        width: 180,
        height: 60,
        right: x + 180,
        bottom: y + 60,
        toJSON: () => ({}),
      }) as DOMRect;
  });
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  del.mockReset().mockResolvedValue(undefined);
  resetFocusMemory();
  resetLibraryMemory();
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("mkt:locale", "en");
  serve();
});

afterEach(() => {
  vi.useRealTimers();
});

describe.each([
  ["en", E, false],
  ["ar", A, true],
] as const)("a library's tabs (%s)", (locale, T, rtl) => {
  beforeEach(() => localStorage.setItem("mkt:locale", locale));

  it("lands on the tab row; the arrow toward the next tab shows it; the layout mirrors", async () => {
    renderBig("/big/library/L1");
    const lib = await screen.findByTestId("big-library");
    expect(screen.getByTestId("big-screen")).toHaveAttribute("dir", rtl ? "rtl" : "ltr");
    const tabs = within(lib).getAllByRole("tab");
    expect(tabs.map((x) => x.textContent)).toEqual([
      T["library.tab.recommended"],
      T["library.tab.library"],
      T["library.tab.collections"],
      T["library.tab.playlists"],
      T["library.tab.categories"],
    ]);
    await waitFor(() => expect(focused()).toBe("ltab:recommended"));
    layRow(tabs, 100, rtl);
    // Arabic: the next tab lies to the left, so Left reaches it (Left and Right swap)
    key(rtl ? "ArrowLeft" : "ArrowRight");
    expect(focused()).toBe("ltab:library");
    await waitFor(() => expect(where).toBe("/big/library/L1?tab=library"));
    expect(await screen.findByTestId("big-library-library")).toBeInTheDocument();
    key(rtl ? "ArrowRight" : "ArrowLeft");
    expect(focused()).toBe("ltab:recommended");
    await waitFor(() => expect(where).toBe("/big/library/L1"));
  });

  it("the letter rail lists the reader's script first", async () => {
    renderBig("/big/library/L1?tab=library");
    const rail = await screen.findByTestId("big-letter-rail");
    await waitFor(() => expect(within(rail).getAllByRole("button").length).toBe(5));
    const letters = within(rail)
      .getAllByRole("button")
      .slice(1)
      .map((b) => b.textContent);
    expect(letters).toEqual(rtl ? ["ا", "ب", "A", "B"] : ["A", "B", "ا", "ب"]);
    fireEvent.click(within(rail).getByRole("button", { name: "ب" }));
    await waitFor(() =>
      expect(where).toBe(`/big/library/L1?tab=library&letter=${encodeURIComponent("ب")}`)
    );
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(expect.stringContaining(`letter=${encodeURIComponent("ب")}`))
    );
  });
});

describe("the remote on a library (Epic 56 §6.5)", () => {
  it("Back steps out in three: the tab's top, the tab row, out of the library; then one confirm to leave", async () => {
    renderBig("/big/library/L1?tab=library");
    await screen.findByText("Second");
    act(() => el("lv:v7").focus());
    key("Escape");
    // the top of the tab: its first control
    expect(focused()).toBe("show:videos");
    key("Escape");
    expect(focused()).toBe("ltab:library");
    key("Escape");
    // out of the library (opened as a top screen: Home)
    await waitFor(() => expect(where).toBe("/big"));
    await screen.findByTestId("big-home");
    key("Escape");
    expect(await screen.findByTestId("big-exit")).toBeInTheDocument();
  });

  it("each tab keeps its focus: Down from the tab row returns to the card left", async () => {
    renderBig("/big/library/L1?tab=library");
    await screen.findByText("Third");
    act(() => el("lv:v8").focus());
    act(() => el("ltab:collections").focus());
    await screen.findByTestId("big-library-collections");
    act(() => el("ltab:library").focus());
    await screen.findByText("Third");
    key("ArrowDown");
    expect(focused()).toBe("lv:v8");
  });

  it("Play/Pause on a focused video plays it at once, from where the user left it", async () => {
    post.mockResolvedValue({
      id: "Q",
      shuffle: false,
      current_pos: 0,
      version: 1,
      source_done: true,
      total_estimate: 1,
      items: [{ pos: 0, origin: "source", id: "v6" }],
    });
    renderBig("/big/library/L1?tab=library");
    await screen.findByText("First");
    act(() => el("lv:v6").focus());
    key("MediaPlayPause");
    await waitFor(() => expect(where).toBe("/big/watch/v6?t=50"));
    expect(post).toHaveBeenCalledWith(
      "/api/me/play-queues",
      expect.objectContaining({ source: { kind: "video", id: "v6" }, shuffle: false })
    );
  });

  it("a short OK opens the card; a long OK opens its actions", async () => {
    renderBig("/big/library/L1?tab=library");
    await screen.findByText("First");
    vi.useFakeTimers({ shouldAdvanceTime: true });
    act(() => el("lv:v7").focus());
    fireEvent.keyDown(window, { key: "Enter" });
    act(() => {
      vi.advanceTimersByTime(LONG_PRESS_MS + 50);
    });
    fireEvent.keyDown(window, { key: "Enter", repeat: true });
    fireEvent.keyUp(window, { key: "Enter" });
    const menu = await screen.findByTestId("big-card-menu");
    expect(within(menu).getByRole("button", { name: E["playQueue.playNext"] })).toBeInTheDocument();
    expect(within(menu).getByRole("button", { name: E["playQueue.add"] })).toBeInTheDocument();
    expect(within(menu).getByRole("button", { name: E["playlists.add"] })).toBeInTheDocument();
    post.mockResolvedValue({ updated: 1 });
    fireEvent.click(within(menu).getByRole("button", { name: E["video.markWatched"] }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/me/watched", { video_ids: ["v7"], watched: true })
    );
    expect(await screen.findByTestId("big-notice")).toHaveTextContent(E["video.markedWatched"]!);
    // still on the library; a short press now opens the card
    act(() => el("lv:v7").focus());
    fireEvent.keyDown(window, { key: "Enter" });
    fireEvent.keyUp(window, { key: "Enter" });
    await waitFor(() => expect(where).toBe("/big/video/v7"));
  });

  it("the menu key opens a card's actions; Back closes them and focus returns to the card", async () => {
    renderBig("/big/library/L1?tab=library");
    await screen.findByText("First");
    act(() => el("lv:v6").focus());
    key("ContextMenu");
    expect(await screen.findByTestId("big-card-menu")).toBeInTheDocument();
    key("Escape");
    await waitFor(() => expect(screen.queryByTestId("big-card-menu")).toBeNull());
    expect(focused()).toBe("lv:v6");
  });

  it("Play plays the Library tab's view in order, with its sort and filters", async () => {
    post.mockResolvedValue({
      id: "Q",
      shuffle: false,
      current_pos: 0,
      version: 1,
      source_done: false,
      total_estimate: 2,
      items: [{ pos: 0, origin: "source", id: "v6" }],
    });
    renderBig("/big/library/L1?tab=library&sort=az&watched=unwatched");
    const lib = await screen.findByTestId("big-library");
    await screen.findByText("First");
    expect(get).toHaveBeenCalledWith(
      expect.stringMatching(/sort=title&order=asc&watched=unwatched/)
    );
    expect(
      within(lib).getByRole("button", { name: E["library.filter.count"]!.replace("{n}", "1") })
    ).toBeInTheDocument();
    fireEvent.click(within(lib).getByRole("button", { name: `▶ ${E["big.play"]}` }));
    await waitFor(() => expect(where).toBe("/big/watch/v6"));
    expect(post).toHaveBeenCalledWith(
      "/api/me/play-queues",
      expect.objectContaining({
        source: {
          kind: "library",
          id: "L1",
          filters: { watched: "unwatched", sort: "title", order: "asc" },
        },
        shuffle: false,
      })
    );
  });

  it("Show Series groups the grid by series; Sort and Filter open their panels", async () => {
    serve({
      "/api/videos?": (url: string) =>
        url.includes("group=series")
          ? {
              items: [
                { kind: "series", id: "S1", name: "Tafsir al-Fatiha", episode_count: 3 },
                { kind: "video", id: "v9", title: "Loose" },
              ],
              next: null,
            }
          : GRID,
    });
    renderBig("/big/library/L1?tab=library");
    const lib = await screen.findByTestId("big-library");
    await screen.findByText("First");
    fireEvent.click(within(lib).getByRole("button", { name: E["library.show.series"] }));
    expect(await screen.findByText("Tafsir al-Fatiha")).toBeInTheDocument();
    expect(screen.getByText("Loose")).toBeInTheDocument();
    expect(where).toBe("/big/library/L1?tab=library&show=series");
    fireEvent.click(within(lib).getByRole("button", { name: new RegExp(E["library.sort"]!) }));
    const sort = await screen.findByTestId("big-sort");
    fireEvent.click(within(sort).getByRole("button", { name: E["library.sort.az"] }));
    await waitFor(() => expect(where).toBe("/big/library/L1?tab=library&show=series&sort=az"));
    expect(get).toHaveBeenCalledWith(expect.stringMatching(/group=series&sort=name/));
  });

  it("Categories open the Library tab filtered", async () => {
    renderBig("/big/library/L1?tab=categories");
    const tab = await screen.findByTestId("big-library-categories");
    expect(
      await within(tab).findByRole("heading", { name: E["library.categories.speakers"] })
    ).toBeInTheDocument();
    fireEvent.click(within(tab).getByText("Sheikh A"));
    await waitFor(() => expect(where).toBe("/big/library/L1?tab=library&speaker=Sheikh+A"));
  });
});

describe("collections and playlists", () => {
  it("the Collections tab badges each kind; Play/Pause on a group plays it; a card opens its page", async () => {
    post.mockResolvedValue({
      id: "Q",
      shuffle: false,
      current_pos: 0,
      version: 1,
      source_done: false,
      total_estimate: 2,
      items: [{ pos: 0, origin: "source", id: "v7" }],
    });
    renderBig("/big/library/L1?tab=collections");
    const tab = await screen.findByTestId("big-library-collections");
    await within(tab).findByText("Favourites");
    expect(within(tab).getByText(E["collections.badge.youtubeChannel"]!)).toBeInTheDocument();
    // no Play and Shuffle in the header on Collections (each card's page has them)
    expect(screen.queryByRole("button", { name: `▶ ${E["big.play"]}` })).toBeNull();
    act(() => el("col:topic:G1").focus());
    key("MediaPlayPause");
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        "/api/me/play-queues",
        expect.objectContaining({ source: { kind: "collection", id: "topic:G1" } })
      )
    );
  });

  it("a collection's page: count, total length, Play, Shuffle; an item plays the list from it", async () => {
    post.mockResolvedValue({
      id: "Q",
      shuffle: false,
      current_pos: 1,
      version: 1,
      source_done: true,
      total_estimate: 2,
      items: [
        { pos: 0, origin: "source", id: "v6" },
        { pos: 1, origin: "source", id: "v7" },
      ],
    });
    renderBig("/big/collection/C1");
    const page = await screen.findByTestId("big-collection:C1");
    expect(within(page).getByRole("heading", { name: "Favourites" })).toBeInTheDocument();
    expect(within(page).getByText("1 h 0 min")).toBeInTheDocument();
    expect(within(page).getByRole("button", { name: `⤮ ${E["big.shuffle"]}` })).toBeEnabled();
    fireEvent.click(within(page).getByText("Second"));
    await waitFor(() => expect(where).toBe("/big/watch/v7"));
    expect(post).toHaveBeenCalledWith(
      "/api/me/play-queues",
      expect.objectContaining({ source: { kind: "collection", id: "C1" }, start_video_id: "v7" })
    );
  });

  it("the Playlists tab shows the user's lists with a Smart badge", async () => {
    renderBig("/big/library/L1?tab=playlists");
    const tab = await screen.findByTestId("big-library-playlists");
    expect(await within(tab).findByText("Evening")).toBeInTheDocument();
    expect(within(tab).getByText(E["collections.badge.smart"]!)).toBeInTheDocument();
    fireEvent.click(within(tab).getByText("Evening"));
    await waitFor(() => expect(where).toBe("/big/playlist/P1"));
  });
});

describe("a series", () => {
  it("Continue, Shuffle, Mark watched; season pills with the season's Play, Shuffle and Mark season watched", async () => {
    post.mockResolvedValue({ updated: 1 });
    renderBig("/big/series/S1");
    const s = await screen.findByTestId("big-series");
    expect(
      within(s).getByRole("button", { name: new RegExp(`${E["big.series.start"]}: Lesson two`) })
    ).toBeInTheDocument();
    expect(
      within(s).getByRole("button", { name: new RegExp(E["series.markWatched"]!) })
    ).toBeInTheDocument();
    fireEvent.click(within(s).getByRole("button", { name: E["big.season"]!.replace("{n}", "2") }));
    expect(await within(s).findByText("Lesson three")).toBeInTheDocument();
    fireEvent.click(
      within(s).getByRole("button", { name: new RegExp(E["series.markSeasonWatched"]!) })
    );
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/me/watched", { video_ids: ["e3"], watched: true })
    );
    post.mockResolvedValue({
      id: "Q",
      shuffle: true,
      current_pos: 0,
      version: 1,
      source_done: true,
      total_estimate: 1,
      items: [{ pos: 0, origin: "source", id: "e3" }],
    });
    fireEvent.click(
      within(s).getByRole("button", { name: new RegExp(E["series.season.shuffle"]!) })
    );
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        "/api/me/play-queues",
        expect.objectContaining({ source: { kind: "season", id: "SS2" }, shuffle: true })
      )
    );
  });
});

describe("the player's Up next and Shuffle again (Epic 56 story 56.11)", () => {
  beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(() => Promise.resolve());
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  });

  it("shows the next lesson in the last 30 s without pausing; Cancel keeps it away", async () => {
    post.mockImplementation((url: string) =>
      url === "/api/stream/sessions"
        ? Promise.resolve({
            session_id: "s",
            mode: "direct",
            direct_url: "/m.mp4",
            expires_at: "2099-01-01T00:00:00Z",
          })
        : Promise.resolve({})
    );
    renderBig("/big/watch/e2?series=S1", ["/big/series/S1"]);
    const w = await screen.findByTestId("big-watch");
    const video = (await within(w).findByTestId("mkt-video")) as HTMLVideoElement;
    await within(w).findByRole("button", { name: new RegExp(E["big.player.nextLesson"]!) });
    Object.defineProperty(video, "duration", { configurable: true, value: 600 });
    video.currentTime = 580;
    fireEvent(video, new Event("timeupdate"));
    const card = await screen.findByTestId("big-upnext");
    expect(card).toHaveTextContent("Lesson three");
    // no countdown before the end
    expect(card).toHaveTextContent(E["big.upNext"]!);
    expect(HTMLMediaElement.prototype.pause).not.toHaveBeenCalled();
    fireEvent.click(within(card).getByRole("button", { name: E["common.cancel"] }));
    await waitFor(() => expect(screen.queryByTestId("big-upnext")).toBeNull());
    video.currentTime = 590;
    fireEvent(video, new Event("timeupdate"));
    fireEvent(video, new Event("ended"));
    expect(screen.queryByTestId("big-upnext")).toBeNull();
  });

  it("a shuffled queue offers Shuffle again", async () => {
    sessionStorage.setItem(
      "mkt:playQueue",
      JSON.stringify({
        server: {
          id: "Q",
          source: { kind: "library", id: "L1" },
          shuffle: true,
          current_pos: 0,
          version: 1,
          source_done: false,
          total_estimate: 9,
          items: [{ pos: 0, origin: "source", id: "v6" }],
        },
      })
    );
    post.mockImplementation((url: string) =>
      url === "/api/stream/sessions"
        ? Promise.resolve({
            session_id: "s",
            mode: "direct",
            direct_url: "/m.mp4",
            expires_at: "2099-01-01T00:00:00Z",
          })
        : Promise.resolve({
            id: "Q",
            source: { kind: "library", id: "L1" },
            shuffle: true,
            current_pos: 0,
            version: 2,
            source_done: false,
            total_estimate: 9,
            items: [{ pos: 0, origin: "source", id: "v6" }],
          })
    );
    get.mockImplementation((url: string) =>
      url.startsWith("/api/me/play-queues/Q")
        ? Promise.resolve({ id: "Q", shuffle: true, current_pos: 0, version: 1, items: [] })
        : /\/chapters$/.test(url)
          ? Promise.resolve({ items: [] })
          : Promise.resolve({ id: "v6", title: "First" })
    );
    renderBig("/big/watch/v6");
    const w = await screen.findByTestId("big-watch");
    const again = await within(w).findByRole("button", {
      name: new RegExp(E["player.shuffleAgain"]!),
    });
    fireEvent.click(again);
    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/me/play-queues/Q/reshuffle"));
  });
});

describe("the view's rules (pure)", () => {
  it("reads and writes the URL, dropping defaults and unknown values", () => {
    const v = readView(
      new URLSearchParams("tab=library&show=series&sort=az&letter=ab&watched=x&speaker=S")
    );
    expect(v).toEqual({ tab: "library", show: "series", sort: "az", filters: { speaker: "S" } });
    expect(writeView(v)).toBe("tab=library&show=series&sort=az&speaker=S");
    expect(readView(new URLSearchParams("tab=nope&sort=plays&show=series")).tab).toBe(
      "recommended"
    );
    // plays is a videos sort, not a series one
    expect(readView(new URLSearchParams("show=series&sort=plays")).sort).toBe("newest");
    expect(writeView(readView(new URLSearchParams("")))).toBe("");
  });

  it("asks GET /api/videos for the view, and plays it in the same order", () => {
    const videos = readView(new URLSearchParams("sort=longest&language=ar&letter=A"));
    expect(videosQuery("L1", videos, 48).toString()).toBe(
      "library=L1&sort=duration_sec&order=desc&language=ar&letter=A&limit=48"
    );
    expect(queueFilters(videos)).toEqual({
      language: "ar",
      letter: "A",
      sort: "duration_sec",
      order: "desc",
    });
    expect(filterCount(videos)).toBe(1);
    const series = readView(new URLSearchParams("show=series&sort=az"));
    expect(videosQuery("L1", series, 48).toString()).toBe(
      "library=L1&group=series&sort=name&limit=48"
    );
    expect(queueFilters(series)).toEqual({ sort: "title", order: "asc" });
  });

  it("orders the rail: the reader's script first, Arabic in its alphabet's order, the rest last", () => {
    const letters = ["#", "ي", "B", "أ", "ب", "A", "ا"];
    expect(railLetters(letters, "en")).toEqual(["A", "B", "ا", "أ", "ب", "ي", "#"]);
    expect(railLetters(letters, "ar")).toEqual(["ا", "أ", "ب", "ي", "A", "B", "#"]);
  });

  it("shows Play and Shuffle in the header except on Collections and Playlists", () => {
    expect(headerPlays("recommended")).toBe(true);
    expect(headerPlays("library")).toBe(true);
    expect(headerPlays("categories")).toBe(true);
    expect(headerPlays("collections")).toBe(false);
    expect(headerPlays("playlists")).toBe(false);
  });

  it("a long press opens the menu once; a short one clicks", () => {
    vi.useFakeTimers();
    const short = vi.fn();
    const long = vi.fn();
    const lp = new LongPress(short, long);
    lp.down(false);
    vi.advanceTimersByTime(LONG_PRESS_MS - 10);
    lp.up();
    expect(short).toHaveBeenCalledTimes(1);
    expect(long).not.toHaveBeenCalled();
    lp.down(false);
    lp.down(true);
    vi.advanceTimersByTime(LONG_PRESS_MS);
    lp.up();
    expect(long).toHaveBeenCalledTimes(1);
    expect(short).toHaveBeenCalledTimes(1);
  });
});
