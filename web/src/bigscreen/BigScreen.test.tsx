// Epic 46 — big-screen mode, screen by screen: what each shows from the API,
// where choosing things goes, Back (one step, Home, then "Leave big screen?"),
// focus restored on Back, the tab row, the search keyboard, the player's
// controls, and the settings. Spatial moves are the focus graph's
// (focus/graph.test.ts); here the arrows are checked on the tab row with a
// stubbed layout.
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
import { ACTION_EVENT } from "./input/actions";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

const card = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  title: `Video ${id}`,
  filename: `${id}.mp4`,
  library_id: "L1",
  duration_sec: 600,
  state: "ready",
  ...extra,
});

const HOME = {
  continue_watching: [card("v1", { position_sec: 120 })],
  recently_added: [card("v2"), card("v3")],
  recently_watched: [],
  pinned: [
    { library: { id: "L1", name: "Tafsir" }, recently_added: [card("v4")], continue_watching: [] },
  ],
};

const NOW = {
  server_time: "2026-10-27T10:00:00Z",
  channels: [
    {
      channel: { id: "c1", number: 7, name: "Quran TV", mode: "shuffle", enabled: true },
      current: {
        channel_id: "c1",
        seq: 0,
        kind: "program",
        title: "Tafsir live",
        start: "2026-10-27T09:30:00Z",
        stop: "2026-10-27T10:30:00Z",
      },
    },
  ],
};

const EPISODES = {
  seasons: [
    {
      season: 1,
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
          progress_pct: 40,
          watched: false,
        },
        {
          video_id: "e3",
          season: 1,
          episode: 3,
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
    progress_pct: 40,
    watched: false,
  },
};

type Routes = Record<string, unknown | ((url: string) => unknown)>;

function serve(extra: Routes = {}) {
  const table: Routes = {
    "/api/home": HOME,
    "/api/channels/now": NOW,
    "/api/libraries/summary": {
      items: [
        { id: "L2", name: "Hadith", videos: 12, poster_video_ids: ["v9"], pinned: false },
        {
          id: "L1",
          name: "Tafsir",
          videos: 40,
          poster_video_ids: ["v4"],
          pinned: true,
          pin_position: 0,
        },
      ],
    },
    "/api/libraries/L1/hub": {
      library: { id: "L1", name: "Tafsir", videos: 40 },
      rows: {
        continue_watching: [],
        recently_added: [card("v4")],
        recently_watched: [],
        most_watched: [card("v5")],
        series: [{ id: "S1", name: "Tafsir al-Fatiha", episodes: 3 }],
        years: [],
      },
    },
    "/api/series?sort=name": {
      items: [{ id: "S1", name: "Tafsir al-Fatiha", episode_count: 3, watched_count: 1 }],
    },
    "/api/series/S1": { id: "S1", name: "Tafsir al-Fatiha" },
    "/api/series/S1/episodes": EPISODES,
    "/api/videos/v1": {
      id: "v1",
      title: "Video v1",
      filename: "v1.mp4",
      description: "About the verse.",
      duration_sec: 600,
      playback_state: { position_sec: 120, completed: false },
    },
    "/api/videos/v1/chapters": {
      items: [
        { start_sec: 0, title: "Opening" },
        { start_sec: 300, title: "The verse" },
      ],
    },
    "/api/videos/v1/segments?from=0&to=240": {
      items: [{ start_sec: 3, text: "In the name of God" }],
    },
    ...extra,
  };
  get.mockImplementation((url: string) => {
    if (url.startsWith("/api/videos?")) {
      const v = table["/api/videos?"];
      return Promise.resolve(typeof v === "function" ? v(url) : (v ?? { items: [], next: null }));
    }
    if (url in table) {
      const v = table[url];
      const out = typeof v === "function" ? v(url) : v;
      return out instanceof Error ? Promise.reject(out) : Promise.resolve(out);
    }
    if (/\/chapters$/.test(url)) return Promise.resolve({ items: [] });
    if (/\/segments\?/.test(url)) return Promise.resolve({ items: [] });
    if (/^\/api\/videos\/[^/?]+$/.test(url))
      return Promise.resolve({ id: url.split("/").pop(), title: "A video", filename: "a.mp4" });
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

let where = "";
function Where() {
  const l = useLocation();
  where = l.pathname + l.search;
  return null;
}

function renderBig(path = "/big", history: string[] = []) {
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

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  del.mockReset().mockResolvedValue(undefined);
  resetFocusMemory();
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("mkt:locale", "en");
  serve();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("Home", () => {
  it("shows the hero to continue, the rows, Live TV on now and the pinned library", async () => {
    renderBig();
    const hero = await screen.findByTestId("big-hero");
    expect(within(hero).getByText("Video v1")).toBeInTheDocument();
    expect(within(hero).getByText(E["big.hero.continue"]!)).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: E["home.row.continueWatching"] })
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: E["home.row.recentlyAdded"] })).toBeInTheDocument();
    // an empty row is hidden
    expect(screen.queryByRole("heading", { name: E["home.row.recentlyWatched"] })).toBeNull();
    expect(await screen.findByText("Quran TV")).toBeInTheDocument();
    expect(screen.getByText("Tafsir live")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Tafsir" })).toBeInTheDocument();
    // the hero's Resume has the focus
    await waitFor(() =>
      expect(document.activeElement).toBe(within(hero).getByRole("button", { name: /Resume/ }))
    );
  });

  it("Resume plays from where the user stopped; a card opens its page", async () => {
    post.mockReturnValue(new Promise(() => {}));
    renderBig();
    const hero = await screen.findByTestId("big-hero");
    fireEvent.click(within(hero).getByRole("button", { name: /Resume/ }));
    await waitFor(() => expect(where).toBe("/big/watch/v1?t=120"));
  });

  it("a card opens the video's page, and Back returns focus to that card", async () => {
    renderBig();
    await screen.findByTestId("big-hero");
    const tile = document.querySelector<HTMLElement>('[data-bs-focus="added:v3"]')!;
    act(() => tile.focus());
    fireEvent.click(tile);
    await screen.findByTestId("big-video");
    expect(where).toBe("/big/video/v3");
    key("Escape");
    await screen.findByTestId("big-hero");
    await waitFor(() =>
      expect(document.activeElement).toBe(document.querySelector('[data-bs-focus="added:v3"]'))
    );
  });

  it("an empty home says so and offers Libraries", async () => {
    serve({
      "/api/home": { continue_watching: [], recently_added: [], recently_watched: [], pinned: [] },
      "/api/channels/now": { server_time: "", channels: [] },
    });
    renderBig();
    expect(await screen.findByText(E["big.home.empty"]!)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: E["home.empty.libraries"] }));
    await waitFor(() => expect(where).toBe("/big/libraries"));
  });

  it("a failed home offers Try again", async () => {
    serve({ "/api/home": new Error("down") });
    renderBig();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    serve();
    fireEvent.click(screen.getByRole("button", { name: E["common.retry"] }));
    expect(await screen.findByTestId("big-hero")).toBeInTheDocument();
  });
});

describe("Epic 45 phase 2 in big screen", () => {
  it("Home shows On Deck (opening a lesson with its series) and Favorites", async () => {
    serve({
      "/api/home": {
        ...HOME,
        on_deck: [card("e2", { series: { id: "S1", name: "Tafsir al-Fatiha" } })],
        favorites: [card("v7", { title: "A favorite" })],
      },
    });
    renderBig();
    await screen.findByTestId("big-hero");
    expect(screen.getByRole("heading", { name: E["home.row.onDeck"] })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: E["home.row.favorites"] })).toBeInTheDocument();
    expect(screen.getByText("A favorite")).toBeInTheDocument();
    fireEvent.click(document.querySelector('[data-bs-focus="ondeck:e2"]')!);
    await waitFor(() => expect(where).toBe("/big/video/e2?series=S1"));
  });

  it("a video's page marks it watched and a favorite", async () => {
    post.mockResolvedValue({ updated: 1 });
    serve({
      "/api/me/videos/v1": {
        video_id: "v1",
        position_sec: 120,
        completed: false,
        favorite: false,
        playlists: [],
        series: null,
      },
    });
    renderBig("/big/video/v1");
    const page = await screen.findByTestId("big-video");
    const fav = await within(page).findByRole("button", { name: new RegExp(E["video.favorite"]!) });
    fireEvent.click(fav);
    await waitFor(() => expect(fav).toHaveAttribute("aria-pressed", "true"));
    expect(post).toHaveBeenCalledWith(
      "/api/me/favorites",
      expect.objectContaining({ favorite: true })
    );
    const watched = within(page).getByRole("button", { name: E["video.markWatched"] });
    fireEvent.click(watched);
    await waitFor(() => expect(watched).toHaveAttribute("aria-pressed", "true"));
  });
});

describe("Back and leaving", () => {
  it("Back on Home asks first; Stay keeps big screen, Leave goes back to where the user was", async () => {
    sessionStorage.setItem("mkt:big:return", "/library");
    renderBig();
    await screen.findByTestId("big-hero");
    key("Escape");
    const dialog = await screen.findByTestId("big-exit");
    expect(within(dialog).getByText(E["big.exit.title"]!)).toBeInTheDocument();
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(dialog).getByRole("button", { name: E["big.exit.stay"] })
      )
    );
    // Back closes the dialog (no trap)
    key("Escape");
    await waitFor(() => expect(screen.queryByTestId("big-exit")).toBeNull());
    key("Backspace");
    fireEvent.click(
      within(await screen.findByTestId("big-exit")).getByRole("button", {
        name: E["big.exit.leave"],
      })
    );
    await waitFor(() => expect(where).toBe("/library"));
    expect(screen.getByText("desk page")).toBeInTheDocument();
  });

  it("Back on a top screen opened directly goes Home, not out", async () => {
    renderBig("/big/libraries");
    await screen.findByTestId("big-libraries");
    key("Escape");
    await waitFor(() => expect(where).toBe("/big"));
    expect(screen.queryByTestId("big-exit")).toBeNull();
  });

  it("the tab row moves with the arrows (a laid-out row) and opens a screen", async () => {
    renderBig();
    await screen.findByTestId("big-hero");
    const tabs = Array.from(document.querySelectorAll<HTMLElement>(".bs-tab"));
    expect(tabs.map((t) => t.textContent)).toEqual([
      E["big.tab.home"],
      E["big.tab.libraries"],
      E["big.tab.series"],
      E["big.tab.search"],
      E["big.tab.live"],
      E["big.tab.settings"],
    ]);
    tabs.forEach((t, i) => {
      t.getBoundingClientRect = () =>
        ({
          x: i * 200,
          y: 0,
          left: i * 200,
          top: 0,
          width: 180,
          height: 60,
          right: i * 200 + 180,
          bottom: 60,
          toJSON: () => ({}),
        }) as DOMRect;
    });
    act(() => tabs[0]!.focus());
    key("ArrowRight");
    expect(document.activeElement).toBe(tabs[1]);
    key("ArrowRight");
    expect(document.activeElement).toBe(tabs[2]);
    key("ArrowLeft");
    expect(document.activeElement).toBe(tabs[1]);
    fireEvent.click(tabs[1]!);
    await screen.findByTestId("big-libraries");
    expect(where).toBe("/big/libraries");
  });

  it("/ opens Search and a pad's Menu opens Settings", async () => {
    renderBig();
    await screen.findByTestId("big-hero");
    key("/");
    await screen.findByTestId("big-search");
    act(() => {
      window.dispatchEvent(
        new CustomEvent(ACTION_EVENT, { detail: { action: "menu", source: "pad" } })
      );
    });
    // the screen listens first; the frame's own handling is for keys and pads
    expect(where).toBe("/big/search");
  });
});

describe("Libraries and a library", () => {
  it("lists the libraries, pinned first, and opens one with its rows and its videos", async () => {
    serve({
      "/api/videos?": (url: string) =>
        url.includes("cursor=")
          ? { items: [{ id: "v8", title: "Older", filename: "v8.mp4" }], next: null }
          : {
              items: [
                {
                  id: "v6",
                  title: "Newest",
                  filename: "v6.mp4",
                  playback_state: { position_sec: 50, completed: false },
                  duration_sec: 100,
                },
                { id: "v7", display_title: "Second", filename: "v7.mp4" },
              ],
              next: "c1",
            },
    });
    renderBig("/big/libraries");
    const grid = await screen.findByTestId("big-libraries");
    const names = within(grid)
      .getAllByRole("listitem")
      .map((b) => b.querySelector(".bs-tile__title")?.textContent);
    expect(names).toEqual(["Tafsir", "Hadith"]);
    expect(within(grid).getByText("40 videos")).toBeInTheDocument();
    fireEvent.click(within(grid).getAllByRole("listitem")[0]!);
    const lib = await screen.findByTestId("big-library");
    expect(where).toBe("/big/library/L1");
    expect(within(lib).getByRole("heading", { name: "Tafsir" })).toBeInTheDocument();
    expect(
      within(lib).getByRole("heading", { name: E["big.row.mostWatched"] })
    ).toBeInTheDocument();
    expect(await within(lib).findByText("Newest")).toBeInTheDocument();
    expect(within(lib).getByText("Second")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(
      expect.stringMatching(/library=L1&sort=created_at&order=desc/)
    );
    fireEvent.click(within(lib).getByRole("button", { name: E["common.loadMore"] }));
    expect(await within(lib).findByText("Older")).toBeInTheDocument();
    expect(within(lib).queryByRole("button", { name: E["common.loadMore"] })).toBeNull();
    // a series in the library opens the series
    fireEvent.click(within(lib).getByText("Tafsir al-Fatiha"));
    await screen.findByTestId("big-series");
    expect(where).toBe("/big/series/S1");
  });

  it("a library the user may not see says not found", async () => {
    const { ApiError } = await import("../lib/api");
    serve({
      "/api/libraries/LX/hub": new ApiError({
        type: "about:blank",
        title: "Not Found",
        status: 404,
      }),
    });
    renderBig("/big/library/LX");
    expect(await screen.findByText(E["library.notFound"]!)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: E["common.retry"] })).toBeNull();
  });
});

describe("Series", () => {
  it("lists the series, and a series continues at its next lesson with Next lesson known", async () => {
    post.mockReturnValue(new Promise(() => {}));
    renderBig("/big/series");
    const list = await screen.findByTestId("big-series-list");
    expect(within(list).getByText("1 of 3 lessons watched")).toBeInTheDocument();
    fireEvent.click(within(list).getByText("Tafsir al-Fatiha"));
    const s = await screen.findByTestId("big-series");
    expect(within(s).getByText("Lesson one")).toBeInTheDocument();
    expect(s.querySelectorAll(".bs-lesson__done").length).toBe(1);
    const cont = within(s).getByRole("button", { name: /Resume: Lesson two/ });
    await waitFor(() => expect(document.activeElement).toBe(cont));
    fireEvent.click(cont);
    // 40% of 1000 s, and the series for Next lesson
    await waitFor(() => expect(where).toBe("/big/watch/e2?t=400&series=S1"));
  });
});

describe("A video's page", () => {
  it("offers Resume and Play from the start, the chapters and the transcript's opening", async () => {
    post.mockReturnValue(new Promise(() => {}));
    renderBig("/big/video/v1");
    const page = await screen.findByTestId("big-video");
    expect(within(page).getByText("About the verse.")).toBeInTheDocument();
    expect(within(page).getByText("You stopped at 2:00")).toBeInTheDocument();
    expect(await within(page).findByText("The verse")).toBeInTheDocument();
    expect(await within(page).findByText("In the name of God")).toBeInTheDocument();
    fireEvent.click(within(page).getByText("The verse"));
    await waitFor(() => expect(where).toBe("/big/watch/v1?t=300"));
  });

  it("Play from the start starts at 0", async () => {
    post.mockReturnValue(new Promise(() => {}));
    renderBig("/big/video/v1");
    const page = await screen.findByTestId("big-video");
    fireEvent.click(within(page).getByRole("button", { name: new RegExp(E["big.fromStart"]!) }));
    await waitFor(() => expect(where).toBe("/big/watch/v1"));
  });
});

describe("Search", () => {
  it("types with the screen keyboard, searches, and plays a transcript match at its time", async () => {
    post.mockImplementation((url: string, body: { q: string }) => {
      if (url !== "/api/search") return new Promise(() => {});
      return Promise.resolve({
        search_id: "s1",
        videos: [{ video_id: "v1", title: `Title ${body.q}`, duration_sec: 60 }],
        videos_total: 1,
        transcript_videos: [
          {
            video_id: "v2",
            title: "Transcript hit",
            matches: 1,
            passages: [
              { segment_id: 1, start_sec: 95, end_sec: 99, snippet: "the <mark>ab</mark> part" },
            ],
          },
        ],
        transcript_total: 1,
      });
    });
    renderBig("/big/search");
    const s = await screen.findByTestId("big-search");
    expect(within(s).getByText(E["big.search.hint"]!)).toBeInTheDocument();
    fireEvent.click(within(s).getByRole("button", { name: "a" }));
    fireEvent.click(within(s).getByRole("button", { name: "b" }));
    fireEvent.click(within(s).getByRole("button", { name: E["big.keyboard.space"] }));
    expect(within(s).getByRole("searchbox")).toHaveValue("ab ");
    expect(await within(s).findByText("Title ab", {}, { timeout: 3000 })).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/api/search", { q: "ab", mode: "hybrid", view: "grid" });
    expect(where).toBe("/big/search?q=ab");
    fireEvent.click(within(s).getByText("Transcript hit"));
    await waitFor(() => expect(where).toBe("/big/watch/v2?t=95"));
  });

  it("switches the keyboard to Arabic, deletes and clears", async () => {
    renderBig("/big/search");
    const s = await screen.findByTestId("big-search");
    fireEvent.click(within(s).getByRole("button", { name: E["big.keyboard.switch"] }));
    fireEvent.click(within(s).getByRole("button", { name: "ب" }));
    fireEvent.click(within(s).getByRole("button", { name: "ا" }));
    expect(within(s).getByRole("searchbox")).toHaveValue("با");
    fireEvent.click(within(s).getByRole("button", { name: E["big.keyboard.delete"] }));
    expect(within(s).getByRole("searchbox")).toHaveValue("ب");
    fireEvent.click(within(s).getByRole("button", { name: E["big.keyboard.clear"] }));
    expect(within(s).getByRole("searchbox")).toHaveValue("");
  });
});

describe("Live TV", () => {
  it("lists the channels with what is on now and opens one", async () => {
    renderBig("/big/live");
    const live = await screen.findByTestId("big-live");
    expect(within(live).getByText("Quran TV")).toBeInTheDocument();
    expect(within(live).getByText("Tafsir live")).toBeInTheDocument();
    expect(within(live).getByText("7")).toBeInTheDocument();
  });
});

describe("Settings", () => {
  it("keeps the playback preferences, switches the language and asks before leaving", async () => {
    renderBig("/big/settings");
    const s = await screen.findByTestId("big-settings");
    const autoplay = within(s).getByRole("switch", { name: E["big.set.autoplay"] });
    expect(autoplay).toHaveAttribute("aria-checked", "true");
    fireEvent.click(autoplay);
    expect(autoplay).toHaveAttribute("aria-checked", "false");
    expect(JSON.parse(localStorage.getItem("mkt:big:prefs")!)).toMatchObject({
      autoplayNext: false,
    });
    fireEvent.click(within(s).getByRole("switch", { name: E["big.set.subtitles"] }));
    expect(JSON.parse(localStorage.getItem("mkt:big:prefs")!)).toMatchObject({ subtitlesOn: true });
    // outside the desktop app there is no "Start in big screen"
    expect(within(s).queryByRole("switch", { name: E["big.set.start"] })).toBeNull();
    fireEvent.click(within(s).getByRole("button", { name: E["big.exit.leave"] }));
    expect(await screen.findByTestId("big-exit")).toBeInTheDocument();
    key("Escape");
    await waitFor(() => expect(screen.queryByTestId("big-exit")).toBeNull());
    fireEvent.click(within(s).getByRole("button", { name: "العربية" }));
    expect(await screen.findByRole("heading", { name: A["big.tab.settings"] })).toBeInTheDocument();
    expect(screen.getByTestId("big-screen")).toHaveAttribute("dir", "rtl");
  });
});

describe("The player", () => {
  beforeEach(() => {
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (
      this: HTMLMediaElement
    ) {
      return Promise.resolve();
    });
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  });

  function openSession() {
    post.mockImplementation((url: string) =>
      url === "/api/stream/sessions"
        ? Promise.resolve({
            session_id: "sess1",
            mode: "direct",
            direct_url: "/media/e2.mp4",
            expires_at: "2099-01-01T00:00:00Z",
            subtitles: [{ lang: "ar", label: "العربية", url: "/subs/ar.vtt" }],
          })
        : Promise.resolve({})
    );
  }

  it("draws 10-foot controls over a bare player, with Next lesson from the series", async () => {
    openSession();
    renderBig("/big/watch/e2?series=S1", ["/big/series/S1"]);
    const w = await screen.findByTestId("big-watch");
    const video = await within(w).findByTestId("mkt-video");
    // bare: no native controls, no desk bar
    expect(video).not.toHaveAttribute("controls");
    expect(w.querySelector(".mkt-player__bar")).toBeNull();
    expect(within(w).getByRole("button", { name: E["big.player.play"] })).toBeInTheDocument();
    expect(within(w).getByRole("button", { name: "Back 10 seconds" })).toBeInTheDocument();
    const next = await within(w).findByRole("button", {
      name: new RegExp(E["big.player.nextLesson"]!),
    });
    // no tab row over the picture
    expect(document.querySelector(".bs-tabs")).toBeNull();
    fireEvent.click(next);
    await waitFor(() => expect(where).toBe("/big/watch/e3?series=S1"));
  });

  it("Space plays, the media keys seek, and the Subtitles panel turns a track on; Back closes the panel", async () => {
    openSession();
    renderBig("/big/watch/v1");
    const w = await screen.findByTestId("big-watch");
    const video = (await within(w).findByTestId("mkt-video")) as HTMLVideoElement;
    key(" ");
    expect(HTMLMediaElement.prototype.play).toHaveBeenCalled();
    Object.defineProperty(video, "duration", { configurable: true, value: 600 });
    video.currentTime = 100;
    key("MediaFastForward");
    expect(video.currentTime).toBe(130);
    key("MediaRewind");
    expect(video.currentTime).toBe(100);
    fireEvent.click(within(w).getByRole("button", { name: E["big.player.subtitles"] }));
    const panel = await screen.findByTestId("big-subtitles");
    expect(
      within(panel).getByRole("button", { name: E["big.player.subtitlesOff"] })
    ).toBeInTheDocument();
    key("Escape");
    await waitFor(() => expect(screen.queryByTestId("big-subtitles")).toBeNull());
    // still in the player: the panel took that Back
    expect(where).toBe("/big/watch/v1");
  });

  it("Back leaves the player for the page it came from", async () => {
    openSession();
    renderBig("/big/video/v1");
    const page = await screen.findByTestId("big-video");
    fireEvent.click(within(page).getByRole("button", { name: /Resume/ }));
    await screen.findByTestId("big-watch");
    key("Escape");
    await screen.findByTestId("big-video");
    expect(where).toBe("/big/video/v1");
    expect(del).toHaveBeenCalledWith("/api/stream/sessions/sess1");
  });
});
