// The mock home the TV build's Playwright tests run against (story 58.08 T03, T04):
// every /api call answered from the tables below, with test data only. A GET it
// does not know answers an empty list and is recorded (unknown()), so a test can
// fail on a screen asking for something new.
import type { Page, Route } from "@playwright/test";

const card = (id: string, title: string, extra: Record<string, unknown> = {}) => ({
  id,
  title,
  filename: `${id}.mp4`,
  library_id: "L1",
  duration_sec: 1500,
  state: "ready",
  ...extra,
});

const TITLES = [
  "Opening the Book",
  "The Night Journey",
  "Patience and Gratitude",
  "Stories of the Prophets",
  "The Cave",
  "Mercy",
  "Light upon Light",
  "The Heights",
  "Ease after Hardship",
  "The Dawn",
  "Abraham's Questions",
  "Yusuf's Dream",
];
const VIDEOS = TITLES.map((t, i) =>
  card(`v${i + 1}`, t, {
    published_at: `2025-0${(i % 9) + 1}-10T00:00:00Z`,
    ...(i === 0 ? { position_sec: 600 } : {}),
    ...(i === 2 ? { completed: true } : {}),
  })
);

const EPISODES = {
  seasons: [1, 2].map((season) => ({
    season,
    episodes: [1, 2, 3, 4].map((episode) => ({
      video_id: `e${season}${episode}`,
      season,
      episode,
      title: `Lesson ${episode}`,
      duration_sec: 1800,
      progress_pct: season === 1 && episode === 1 ? 100 : season === 1 && episode === 2 ? 40 : 0,
      watched: season === 1 && episode === 1,
    })),
  })),
  next_episode: {
    video_id: "e12",
    season: 1,
    episode: 2,
    title: "Lesson 2",
    duration_sec: 1800,
    progress_pct: 40,
    watched: false,
  },
};

const LIBRARIES = {
  items: [
    { id: "L1", name: "Tafsir", videos: 12, poster_video_ids: ["v1", "v2", "v3"], pinned: true },
    { id: "L2", name: "Hadith", videos: 4, poster_video_ids: ["v4"], pinned: false },
  ],
};

const HUB = {
  library: { id: "L1", name: "Tafsir", videos: 12 },
  rows: {
    continue_watching: [VIDEOS[0]],
    recently_added: VIDEOS.slice(1, 8),
    recently_watched: [],
    most_watched: VIDEOS.slice(4, 10),
    series: [{ id: "S1", name: "Tafsir al-Fatiha", episodes: 8, poster_video_id: "v2" }],
    years: [],
  },
};

const TABLE: Record<string, unknown> = {
  "/api/auth/me": { user: { id: "u-tv", username: "amal", is_admin: false } },
  "/api/capabilities": { features: {}, admin: {} },
  "/api/home": {
    continue_watching: [VIDEOS[0]],
    recently_added: VIDEOS.slice(1, 9),
    recently_watched: [VIDEOS[2]],
    pinned: [
      {
        library: { id: "L1", name: "Tafsir" },
        recently_added: VIDEOS.slice(3, 9),
        continue_watching: [],
      },
    ],
  },
  "/api/channels/now": { server_time: new Date().toISOString(), channels: [] },
  "/api/me/play-queues/current": null,
  "/api/libraries/summary": LIBRARIES,
  "/api/libraries/L1/hub": HUB,
  "/api/videos/facets": {
    letters: ["A", "L", "M", "O", "P", "S", "T", "Y", "ا", "ر", "ص"].map((letter) => ({
      letter,
      videos: 1,
    })),
    languages: [
      { code: "ar", videos: 7 },
      { code: "en", videos: 5 },
    ],
    tags: [],
    speakers: [],
  },
  "/api/series": {
    items: [
      {
        id: "S1",
        name: "Tafsir al-Fatiha",
        episode_count: 8,
        watched_count: 1,
        poster_video_id: "v2",
      },
    ],
  },
  "/api/series/S1": { id: "S1", name: "Tafsir al-Fatiha", library_id: "L1" },
  "/api/series/S1/episodes": EPISODES,
  "/api/me/playlists": {
    items: [
      { id: "P1", name: "Before sleep", kind: "manual", videos: 3, poster_video_ids: ["v5", "v6"] },
    ],
  },
  "/api/libraries/L1/collections": {
    items: [
      { id: "C1", kind: "curated", name: "Ramadan", videos: 5, poster_video_ids: ["v7", "v8"] },
    ],
    next: null,
  },
  "/api/libraries/L1/categories": { topics: [], speakers: [], languages: [] },
};

const COLORS = ["#2b3a55", "#4a3b2a", "#2a4a3f", "#4a2a3b", "#3b2a4a", "#2a3f4a"];

/** A poster: a flat SVG in the video's colour (no network, no real art). */
function poster(path: string) {
  const id = path.split("/")[3] ?? "x";
  const c = COLORS[[...id].reduce((a, ch) => a + ch.charCodeAt(0), 0) % COLORS.length];
  return `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="320" height="180" fill="${c}"/><circle cx="160" cy="90" r="34" fill="#c9a24a" opacity=".35"/></svg>`;
}

function videos(url: URL) {
  if (url.searchParams.get("group") === "series")
    return {
      items: [
        {
          kind: "series",
          series: { id: "S1", name: "Tafsir al-Fatiha", episodes: 8, poster_video_id: "v2" },
        },
      ],
      next: null,
    };
  const letter = url.searchParams.get("letter");
  const items = letter ? VIDEOS.filter((v) => v.title.startsWith(letter)) : VIDEOS;
  return { items, next: null, total: items.length };
}

export interface MockHome {
  /** GETs no table answered */
  unknown: string[];
  /** the calls the home took, in order (method and path) */
  calls: string[];
}

export interface MockOptions {
  signedIn?: boolean;
  /** titles sent in place of the test data's (old title → new), e.g. an Arabic title (58.15) */
  rename?: Record<string, string>;
}

export async function mockHome(page: Page, opts: MockOptions = {}): Promise<MockHome> {
  const home: MockHome = { unknown: [], calls: [] };
  const renamed = (text: string) =>
    Object.entries(opts.rename ?? {}).reduce(
      (out, [from, to]) => out.replaceAll(JSON.stringify(from), JSON.stringify(to)),
      text
    );
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: renamed(JSON.stringify(body)) });
  await page.route("**/api/**", (route) => {
    const req = route.request();
    const url = new URL(req.url());
    home.calls.push(`${req.method()} ${url.pathname}${url.search}`);
    if (url.pathname === "/api/auth/me" && opts.signedIn === false)
      return json(route, 401, { error: "unauthorized" });
    if (req.method() !== "GET") return json(route, 200, {});
    if (/\/poster$|\/thumbnail$|\/art\b/.test(url.pathname))
      return route.fulfill({
        status: 200,
        contentType: "image/svg+xml",
        body: poster(url.pathname),
      });
    if (url.pathname === "/api/videos") return json(route, 200, videos(url));
    if (url.pathname in TABLE) return json(route, 200, TABLE[url.pathname]);
    const video = /^\/api\/videos\/([^/]+)$/.exec(url.pathname);
    if (video) {
      const v = VIDEOS.find((x) => x.id === video[1]) ?? card(video[1]!, "Lesson");
      return json(route, 200, { ...v, description: "A lesson from the test library." });
    }
    home.unknown.push(url.pathname);
    return json(route, 200, { items: [], next: null });
  });
  // the live updates socket: none in the mock
  await page.routeWebSocket(/\/ws\//, (ws) => ws.close());
  return home;
}
