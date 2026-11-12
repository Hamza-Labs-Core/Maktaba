#!/usr/bin/env node
// The demo library's answers (story 60.09): `library.json` turned into what a home answers,
// in one language, as a route table in MockHome's format (`routes.json`: method, path,
// status, file; `{id}` matches one segment). Every app's screenshots read these answers:
// the web capture (web/packages/website/scripts/capture-web.mjs) from `buildRoutes`, Apple's
// MockHome and Android's FakeHome from the files written to `routes/<lang>/`.
//
//   node tests/fixtures/demo-library/routes.mjs           # rewrite routes/en and routes/ar
//   node tests/fixtures/demo-library/routes.mjs --check   # fail when routes/ is stale
//
// Times are `{{now}}`, `{{now-N}}` or `{{now+N}}` (seconds), as in Apple's UITests/Fixtures:
// each reader fills them from `library.json`'s `clock` (noon UTC, so the date is the same in
// every zone). An image route names its file under `art/` in `art` instead of `file`.
// Node's standard library only.

import { readFileSync, writeFileSync, mkdirSync, rmSync, readdirSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const ROOT = dirname(fileURLToPath(import.meta.url));
export const LANGS = ["en", "ar"];

export function loadLibrary(root = ROOT) {
  return JSON.parse(readFileSync(join(root, "library.json"), "utf8"));
}

const HOUR = 3600;
const DAY = 86400;
const t = (offset) => (offset === 0 ? "{{now}}" : offset > 0 ? `{{now+${offset}}}` : `{{now${offset}}}`);

/** `{{now…}}` in `text` as RFC 3339 times from `clock` (an ISO time). */
export function fillTimes(text, clock) {
  const base = Date.parse(clock);
  return text.replace(/\{\{now([+-]\d+)?\}\}/g, (_, off) =>
    new Date(base + Number(off ?? 0) * 1000).toISOString().replace(/\.000Z$/, "Z"),
  );
}

/** The route table for `lang`: [{ method, path, status, body?, art? }]. */
export function buildRoutes(lang, lib = loadLibrary()) {
  const L = (s) => s[lang];
  const libs = lib.libraries;
  const videos = lib.videos;
  const byId = new Map(videos.map((v) => [v.id, v]));
  const libName = (id) => L(libs.find((l) => l.id === id).name);
  const routes = [];
  const add = (method, path, body, status = 200) => routes.push({ method, path, status, body });
  const art = (path, file) => routes.push({ method: "GET", path, status: 200, art: file });

  const ext = (v) => (v.library_id === "lib-films" ? "mkv" : "mp4");
  const filename = (v) => `${v.art}.${ext(v)}`;
  const created = (v) => -v.added_days_ago * DAY;
  const lastWatched = (v) => (v.watch ? -Math.round(v.watch.last_watched_hours_ago * HOUR) : null);
  const position = (v) => (v.watch ? (v.watch.completed ? v.duration_sec : (v.watch.position_sec ?? 0)) : 0);
  const series = (v) => lib.series.find((s) => s.id === v.series_id);
  const episodeOf = (v) => {
    const s = series(v);
    if (!s) return null;
    for (const season of s.seasons) {
      const i = season.episodes.indexOf(v.id);
      if (i >= 0) return { season: season.season, episode: i + 1 };
    }
    return null;
  };
  // a home row's item (`/api/home`, a hub's rows)
  const row = (v, extra = {}) => ({
    id: v.id,
    title: L(v.title),
    filename: filename(v),
    library_id: v.library_id,
    duration_sec: v.duration_sec,
    state: "ready",
    created_at: t(created(v)),
    published_at: `${v.year}-06-01T00:00:00Z`,
    ...extra,
  });
  const progressRow = (v) =>
    row(v, {
      position_sec: position(v),
      completed: Boolean(v.watch?.completed),
      last_watched_at: t(lastWatched(v)),
    });
  // a video as `/api/videos` and `/api/videos/{id}` answer it, with no path a screen could show
  const full = (v) => ({
    id: v.id,
    library_id: v.library_id,
    path: "",
    filename: filename(v),
    title: L(v.title),
    description: L(v.synopsis),
    state: "ready",
    detected_language: "en",
    duration_sec: v.duration_sec,
    size_bytes: v.duration_sec * 250000,
    content_hash: v.id.replace(/[^a-z]/g, "").padEnd(64, "0").slice(0, 64),
    created_at: t(created(v)),
    updated_at: t(created(v) + 600),
    published_at: `${v.year}-06-01T00:00:00Z`,
    playable: true,
    tags: [],
    poster_url: `/api/videos/${v.id}/poster`,
    backdrop_url: `/api/videos/${v.id}/backdrop`,
    ...(v.watch
      ? {
          playback_state: {
            position_sec: position(v),
            completed: Boolean(v.watch.completed),
            updated_at: t(lastWatched(v)),
          },
        }
      : {}),
  });

  const newest = [...videos].sort((a, b) => a.added_days_ago - b.added_days_ago);
  const watched = videos.filter((v) => v.watch).sort((a, b) => a.watch.last_watched_hours_ago - b.watch.last_watched_hours_ago);
  const inProgress = watched.filter((v) => !v.watch.completed);
  const inLib = (id) => newest.filter((v) => v.library_id === id);

  // ---- the user, the home --------------------------------------------------------------
  const u = lib.user;
  add("GET", "/api/me", { id: u.id, username: u.username, email: "", display_name: L(lib.household), is_admin: u.is_admin });
  add("GET", "/api/auth/me", {
    user_id: u.id,
    is_admin: u.is_admin,
    libraries: [],
    id: u.id,
    username: u.username,
    user: { id: u.id, username: u.username, is_admin: u.is_admin, must_change_password: false },
    home_name: L(lib.home),
  });

  // ---- home and recommendations --------------------------------------------------------
  const pinned = libs.slice(0, 1);
  add("GET", "/api/home", {
    continue_watching: inProgress.map(progressRow),
    recently_added: newest.map((v) => row(v)),
    recently_watched: watched.map(progressRow),
    pinned: pinned.map((l) => ({
      library: { id: l.id, name: L(l.name) },
      recently_added: inLib(l.id).map((v) => row(v)),
      continue_watching: inLib(l.id).filter((v) => v.watch && !v.watch.completed).map(progressRow),
    })),
  });
  const forYou = newest.filter((v) => !v.watch);
  add("GET", "/api/recommendations", {
    rails: [
      {
        id: "continue",
        title: lang === "ar" ? "متابعة المشاهدة" : "Continue Watching",
        reason_kind: "continue_watching",
        items: inProgress.map((v) => ({
          video_id: v.id,
          position_sec: position(v),
          duration_sec: v.duration_sec,
          remaining_sec: v.duration_sec - position(v),
          last_watched_at: t(lastWatched(v)),
        })),
      },
      {
        id: "for-you",
        title: lang === "ar" ? "مقترحة لكم" : "For You",
        reason_kind: "for_you",
        items: forYou.map((v, i) => ({ video_id: v.id, score: Number((0.95 - i * 0.05).toFixed(2)) })),
      },
    ],
    generated_at: t(0),
    expires_at: t(60),
    cache_hit: false,
  });

  // ---- libraries --------------------------------------------------------------------------
  add("GET", "/api/libraries", {
    items: libs.map((l, i) => ({
      id: l.id,
      name: L(l.name),
      roots: [],
      settings: {},
      created_at: t(-(400 - i) * DAY),
      updated_at: t(-(30 - i) * DAY),
    })),
  });
  add("GET", "/api/libraries/summary", {
    items: libs.map((l, i) => {
      const vs = inLib(l.id);
      return {
        id: l.id,
        name: L(l.name),
        videos: vs.length,
        duration_sec: vs.reduce((n, v) => n + v.duration_sec, 0),
        poster_video_ids: vs.slice(0, 4).map((v) => v.id),
        latest_added_at: t(created(vs[0])),
        pinned: i === 0,
        pin_position: i === 0 ? 0 : null,
      };
    }),
  });
  const pins = { library_ids: pinned.map((l) => l.id), items: pinned.map((l, i) => ({ id: l.id, name: L(l.name), position: i })) };
  add("GET", "/api/me/pinned-libraries", pins);
  add("PUT", "/api/me/pinned-libraries", pins);

  const seriesItem = (s) => {
    const eps = s.seasons.flatMap((x) => x.episodes).map((id) => byId.get(id));
    return {
      id: s.id,
      name: L(s.name),
      library_id: s.library_id,
      year: s.year,
      numbering: "season",
      season_count: s.seasons.length,
      episode_count: eps.length,
      watched_count: eps.filter((v) => v.watch?.completed).length,
      in_progress: eps.filter((v) => v.watch && !v.watch.completed).length,
      poster_video_id: `v-${s.poster}`,
    };
  };
  for (const l of libs) {
    const vs = inLib(l.id);
    const ss = lib.series.filter((s) => s.library_id === l.id);
    const years = [...new Set(vs.map((v) => v.year))].sort((a, b) => b - a);
    add("GET", `/api/libraries/${l.id}/hub`, {
      library: { id: l.id, name: L(l.name), videos: vs.length },
      rows: {
        continue_watching: vs.filter((v) => v.watch && !v.watch.completed).map(progressRow),
        recently_added: vs.map((v) => row(v)),
        recently_watched: vs.filter((v) => v.watch).map(progressRow),
        most_watched: vs.filter((v) => v.watch).map((v) => row(v, { plays: 3 })),
        series: ss.map(seriesItem),
        years: years.map((y) => ({ year: y, videos: vs.filter((v) => v.year === y).length })),
        on_deck: [],
        recently_added_grouped: vs.map((v) => ({ kind: "video", video: row(v) })),
      },
    });
    add("GET", `/api/libraries/${l.id}/series`, {
      library: { id: l.id, name: L(l.name) },
      sort: "name",
      items: ss.map(seriesItem),
      total: ss.length,
      next: null,
    });
  }
  add("GET", "/api/libraries/{id}/collections", { items: [], counts: { all: 0, youtube: 0, plex: 0, curated: 0 }, total: 0, next: null });
  add("GET", "/api/libraries/{id}/categories", {
    speakers: [],
    topics: [],
    languages: [{ value: "en", label: "en", count: videos.length }],
    years: [...new Set(videos.map((v) => v.year))].sort((a, b) => b - a).map((y) => ({ value: String(y), label: String(y), count: videos.filter((v) => v.year === y).length })),
  });

  // ---- videos --------------------------------------------------------------------------
  add("GET", "/api/videos", { items: newest.map(full), next: null });
  add("GET", "/api/videos/facets", {
    languages: [{ code: "en", videos: videos.length }],
    tags: [],
    speakers: [],
    letters: [],
    durations: [{ value: "short", videos: videos.length }],
    added: [{ value: "week", videos: videos.filter((v) => v.added_days_ago <= 7).length }],
    resolutions: [{ value: "hd", videos: videos.length }],
  });
  for (const v of videos) {
    add("GET", `/api/videos/${v.id}`, full(v));
    const s = series(v);
    add("GET", `/api/me/videos/${v.id}`, {
      video_id: v.id,
      position_sec: position(v),
      completed: Boolean(v.watch?.completed),
      favorite: false,
      playlists: [],
      ...(s ? { series: { id: s.id, name: L(s.name) } } : {}),
    });
    // a poster is 16:9, a frame as a home extracts one; `portrait` is the 2:3 crop
    art(`/api/videos/${v.id}/poster`, `${v.art}-backdrop.jpg`);
    art(`/api/videos/${v.id}/portrait`, `${v.art}-poster.jpg`);
    art(`/api/videos/${v.id}/backdrop`, `${v.art}-backdrop.jpg`);
    art(`/api/videos/${v.id}/art`, `${v.art}-backdrop.jpg`);
    art(`/api/videos/${v.id}/thumbnail`, `${v.art}-backdrop.jpg`);
  }
  add("GET", "/api/videos/{id}/chapters", { items: [] });
  add("GET", "/api/videos/{id}/subtitles", {
    items: [
      { id: "sub-en", lang: "en", label: "English", format: "vtt", source: "sidecar", default: lang === "en" },
      { id: "sub-ar", lang: "ar", label: "العربية", format: "vtt", source: "sidecar", default: lang === "ar" },
    ],
  });

  // ---- series ----------------------------------------------------------------------------
  add("GET", "/api/series", { items: lib.series.map(seriesItem) });
  for (const s of lib.series) {
    add("GET", `/api/series/${s.id}`, seriesItem(s));
    const episode = (id) => {
      const v = byId.get(id);
      const e = episodeOf(v);
      return {
        video_id: v.id,
        season: e.season,
        episode: e.episode,
        title: L(v.title),
        thumbnail: `/api/videos/${v.id}/poster`,
        duration_sec: v.duration_sec,
        progress_pct: v.watch ? (v.watch.completed ? 100 : Number(((100 * position(v)) / v.duration_sec).toFixed(2))) : 0,
        watched: Boolean(v.watch?.completed),
      };
    };
    const next = s.seasons.flatMap((x) => x.episodes).find((id) => !byId.get(id).watch?.completed);
    add("GET", `/api/series/${s.id}/episodes`, {
      seasons: s.seasons.map((x) => ({ season: x.season, episodes: x.episodes.map(episode) })),
      numbering: "season",
      sort: "episode",
      next_episode: next ? episode(next) : null,
    });
    art(`/api/series/${s.id}/poster`, `${s.poster}-backdrop.jpg`);
    art(`/api/series/${s.id}/portrait`, `${s.poster}-poster.jpg`);
    art(`/api/series/${s.id}/backdrop`, `${s.poster}-backdrop.jpg`);
  }

  // ---- search ----------------------------------------------------------------------------
  const q = L(lib.search.query);
  const hit = (v) => ({
    video_id: v.id,
    title: L(v.title),
    duration_sec: v.duration_sec,
    library_id: v.library_id,
    library_name: libName(v.library_id),
    poster_url: `/api/videos/${v.id}/poster`,
  });
  const titleHits = lib.search.titles.map((id, i) => ({
    ...hit(byId.get(id)),
    score: Number((0.9 - i * 0.1).toFixed(2)),
    in_transcripts: true,
    title_snippet: L(byId.get(id).title),
  }));
  const passages = lib.search.passages.map((p, i) => ({
    segment_id: 9001 + i,
    video_id: p.video_id,
    start_sec: p.start_sec,
    end_sec: p.start_sec + 4,
    snippet: p.text,
    score: Number((0.08 - i * 0.01).toFixed(3)),
  }));
  const transcriptVideos = [...new Set(passages.map((p) => p.video_id))].map((id) => {
    const mine = passages.filter((p) => p.video_id === id);
    return {
      ...hit(byId.get(id)),
      best_score: mine[0].score,
      matches: mine.length,
      in_title: lib.search.titles.includes(id),
      passages: mine.map(({ video_id, ...p }) => p),
    };
  });
  const search = {
    search_id: "sr-demo",
    expires_at: t(1800),
    q,
    mode: "hybrid",
    filters: {},
    took_ms: { fts: 9, semantic: 14, fusion: 1 },
    page_size: 20,
    passages_page_size: 20,
    videos: titleHits,
    videos_total: titleHits.length,
    transcript_videos: transcriptVideos,
    transcript_total: transcriptVideos.length,
    hits: passages,
    total: passages.length,
  };
  add("POST", "/api/search", search);
  add("GET", "/api/search", search);
  add("GET", "/api/search/results/{sid}", {
    search_id: "sr-demo",
    expires_at: t(1800),
    q,
    mode: "hybrid",
    list: "titles",
    offset: 0,
    limit: 20,
    total: titleHits.length,
    items: titleHits,
  });
  add("GET", "/api/search/suggest", { suggestions: [q, ...lib.search.titles.map((id) => L(byId.get(id).title))] });
  add("GET", "/api/search/recent", {
    items: [
      {
        search_id: "sr-demo",
        q,
        mode: "hybrid",
        created_at: t(-600),
        last_used_at: t(-600),
        expires_at: t(1800),
        title_count: titleHits.length,
        transcript_count: transcriptVideos.length,
        live: true,
      },
    ],
  });

  // ---- live TV: each channel plays its lineup back to back, from 2 h before now ----------
  const GUIDE_START = -2 * HOUR;
  const GUIDE_END = 8 * HOUR;
  const brief = (c) => ({ id: c.id, number: c.number, name: L(c.name), slug: c.id.replace(/^ch-/, ""), logo_url: null });
  const blocksOf = (c) => {
    const blocks = [];
    let at = GUIDE_START - (c.number * 7 * 60); // channels start off step, as a real guide does
    for (let i = 0; at < GUIDE_END; i++) {
      const v = byId.get(c.lineup[i % c.lineup.length]);
      // a programme is the title and the channel's filler, to the next quarter hour
      const len = Math.ceil((v.duration_sec + 60) / 900) * 900;
      const e = episodeOf(v);
      const s = series(v);
      blocks.push({
        channel_id: c.id,
        kind: "program",
        start: t(at),
        stop: t(at + len),
        title: L(v.title),
        sub_title: L(v.title),
        desc: L(v.synopsis),
        poster: `/api/videos/${v.id}/poster`,
        ...(s ? { series: L(s.name), season: e.season, episode: e.episode } : {}),
        on_air: L(v.title),
        strip_title: L(v.title),
        ...(s ? { strip_series: L(s.name) } : {}),
        _start: at,
        _stop: at + len,
      });
      at += len;
    }
    return blocks;
  };
  const strip = ({ _start, _stop, ...b }) => b;
  const now = (c) => {
    const bs = blocksOf(c);
    const i = bs.findIndex((b) => b._start <= 0 && b._stop > 0);
    const cur = bs[i];
    const progress = Number((-cur._start / (cur._stop - cur._start)).toFixed(4));
    return { cur, next: bs[i + 1], progress };
  };
  const channel = (c) => {
    const { cur, progress } = now(c);
    return {
      ...brief(c),
      library_id: c.library_id,
      logo_path: "",
      art: { watermark: { overridden: false, position: "top-right", width_pct: 12, margin_pct: 2 }, loading: { overridden: false }, strip: null },
      category: "general",
      mode: "sequential",
      mode_config: {},
      transition: "cut",
      enabled: true,
      sort_order: c.number,
      created_at: t(-200 * DAY),
      updated_at: t(-10 * DAY),
      now_playing: { title: cur.title, start_at: cur.start, end_at: cur.stop, progress },
      guide_until: t(DAY),
    };
  };
  add("GET", "/api/channels", { items: lib.channels.map(channel) });
  add("GET", "/api/channels/now", {
    server_time: t(0),
    channels: lib.channels.map((c) => {
      const { cur, next, progress } = now(c);
      return {
        channel: brief(c),
        current: { ...strip(cur), is_live: true, progress },
        next: strip(next),
        on_air: true,
      };
    }),
  });
  add("GET", "/api/channels/guide", {
    server_time: t(0),
    start: t(GUIDE_START),
    end: t(GUIDE_END),
    channels: lib.channels.map((c) => ({ channel: brief(c), blocks: blocksOf(c).map(strip) })),
  });
  for (const c of lib.channels) {
    add("GET", `/api/channels/${c.id}`, channel(c));
    add("GET", `/api/channels/${c.id}/guide`, {
      server_time: t(0),
      channel: brief(c),
      blocks: blocksOf(c).map(strip),
      horizon_until: t(DAY),
    });
  }

  // ---- the person's own lists ----------------------------------------------------------
  add("GET", "/api/me/history", {
    items: watched.map((v) => ({
      video_id: v.id,
      title: L(v.title),
      duration_sec: v.duration_sec,
      times_watched: 1,
      total_watch_sec: position(v),
      best_percent: Number(((100 * position(v)) / v.duration_sec).toFixed(1)),
      last_watched_at: t(lastWatched(v)),
      position_sec: position(v),
      completed: Boolean(v.watch.completed),
    })),
    limit: 50,
    offset: 0,
  });
  add("GET", "/api/me/playlists", { items: [] });
  add("POST", "/api/me/watched", { updated: 1 });
  add("POST", "/api/me/favorites", { updated: 1 });
  return routes;
}

/** Writes `lang`'s route table to `dir`: routes.json and one file per answer. */
export function writeRoutes(dir, lang, lib = loadLibrary()) {
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
  const table = buildRoutes(lang, lib).map((r) => {
    if (r.art) return { method: r.method, path: r.path, status: r.status, art: r.art };
    const file =
      `${r.method.toLowerCase()}${r.path.replace(/^\/api/, "").replace(/[{}]/g, "").replace(/\//g, "-")}.json`;
    writeFileSync(join(dir, file), JSON.stringify(r.body, null, 1) + "\n");
    return { method: r.method, path: r.path, status: r.status, file };
  });
  writeFileSync(join(dir, "routes.json"), JSON.stringify(table, null, 1) + "\n");
}

function snapshot(dir) {
  if (!existsSync(dir)) return new Map();
  return new Map(readdirSync(dir).map((f) => [f, readFileSync(join(dir, f), "utf8")]));
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const check = process.argv.includes("--check");
  let stale = [];
  for (const lang of LANGS) {
    const dir = join(ROOT, "routes", lang);
    if (check) {
      const tmp = join(ROOT, "routes", `.check-${lang}-${process.pid}`);
      writeRoutes(tmp, lang);
      const want = snapshot(tmp);
      const have = snapshot(dir);
      rmSync(tmp, { recursive: true, force: true });
      for (const [f, text] of want) if (have.get(f) !== text) stale.push(`routes/${lang}/${f}`);
      for (const f of have.keys()) if (!want.has(f)) stale.push(`routes/${lang}/${f} (no longer generated)`);
    } else {
      writeRoutes(dir, lang);
    }
  }
  if (stale.length) {
    console.error(`demo-library: routes/ is stale; run node tests/fixtures/demo-library/routes.mjs\n  ${stale.join("\n  ")}`);
    process.exit(1);
  }
  if (!check) console.log("demo-library: wrote routes/en and routes/ar");
}
