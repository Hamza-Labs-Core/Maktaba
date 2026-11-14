// A mock Maktaba home and Maktaba Cloud for the channel's screen tests on brs-engine (story
// 58.12 T03). Test data only: two libraries, English and Arabic titles, a series with two
// seasons, collections, a playlist, categories, the letter facets, and posters drawn here.
//
// The home answers as the real one does where the channel reads it (field names from the API's
// handlers and OpenAPI): bearer tokens on every route but discovery, pairing and refresh; a 401
// for an expired access token (the first /api/home of each sign-in, to run the refresh); 404
// for a video the viewer cannot see. The cloud gives a code and approves it on the third poll.
// Every request is logged (no tokens) for the CI report.

import { createServer } from "node:http";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { createCanvas } = require("canvas");

const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
export const HOME_ID = "h1test";
export const LIB = uuid(900);
export const SERIES = uuid(800);
export const HIDDEN = uuid(999);

const titles = [
  "The Long Road", "رحلة إلى الأندلس", "Night Market", "حكايات البحر", "Mountain Light",
  "مدن الذهب", "Quiet Harbour", "الطريق إلى مكة", "River of Stars", "بيت الحكمة",
  "Paper Lanterns", "أسواق دمشق", "Winter Garden", "صوت الصحراء", "Old Maps",
  "Atlas of Clouds", "نجوم الليل", "Desert Bloom", "قصر الحمراء", "Salt and Iron",
];
const colours = ["#2b4c7e", "#7e2b4c", "#2b7e5e", "#7e6a2b", "#4c2b7e", "#2b6f7e"];

export const videos = [];
for (let i = 0; i < 80; i++) {
  videos.push({
    id: uuid(i + 1),
    title: `${titles[i % titles.length]}${i >= titles.length ? ` ${Math.floor(i / titles.length) + 1}` : ""}`,
    filename: `video${i + 1}.mkv`,
    library_id: LIB,
    duration_sec: 1800 + i * 60,
    position_sec: i % 4 === 0 ? 600 : 0,
    completed: false,
    series: i < 12 ? { id: SERIES, name: "حكايات البحر" } : null,
    description: i % 2 ? "قصة عن البحر والسفن والمدن القديمة على الساحل، تُروى عبر أجيال من الصيادين." : "A story of travel and old cities, told across three generations.",
    playable: true,
  });
}

function poster(id) {
  const n = parseInt(id.slice(-4), 10) || 0;
  const c = createCanvas(320, 180);
  const g = c.getContext("2d");
  g.fillStyle = colours[n % colours.length];
  g.fillRect(0, 0, 320, 180);
  g.fillStyle = "rgba(255,255,255,0.25)";
  g.fillRect(20, 120, 280, 8);
  g.fillStyle = "#E8ECF3";
  g.font = "bold 64px sans-serif";
  g.fillText(String(n), 24, 90);
  return c.toBuffer("image/png");
}

function qr() {
  const c = createCanvas(420, 420);
  const g = c.getContext("2d");
  g.fillStyle = "#fff";
  g.fillRect(0, 0, 420, 420);
  g.fillStyle = "#000";
  for (let y = 0; y < 21; y++) for (let x = 0; x < 21; x++) if ((x * 7 + y * 13) % 5 < 2 || (x < 7 && y < 7)) g.fillRect(x * 20, y * 20, 20, 20);
  return c.toBuffer("image/png");
}

function json(res, status, body) {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
}

function readBody(req) {
  return new Promise((resolve) => {
    let s = "";
    req.on("data", (d) => (s += d));
    req.on("end", () => {
      try {
        resolve(s ? JSON.parse(s) : {});
      } catch {
        resolve({});
      }
    });
  });
}

/** Starts the home; log(line) gets each request. */
export function startHome(log) {
  const state = { access: new Set(), refresh: new Set(), expired: new Set(), n: 0, grant: "G1", deviceCode: "dc1", clientHeaders: new Set() };
  const card = (v) => ({ id: v.id, title: v.title, filename: v.filename, library_id: v.library_id, duration_sec: v.duration_sec, position_sec: v.position_sec, completed: v.completed, series: v.series });
  const mint = () => {
    state.n++;
    const a = `A${state.n}`;
    const r = `R${state.n}`;
    state.access.add(a);
    state.refresh.add(r);
    return { access_token: a, refresh_token: r, access_expires_in: 900, refresh_expires_in: 2592000, user_id: "u-viewer" };
  };
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://x");
    const p = url.pathname;
    const auth = (req.headers.authorization || "").replace(/^Bearer /, "");
    state.clientHeaders.add(req.headers["x-maktaba-client"] || "");
    log(`home ${req.method} ${p}${url.search ? " ?" + [...url.searchParams.keys()].join(",") : ""} auth=${auth ? "bearer" : "none"}`);
    const body = req.method === "POST" ? await readBody(req) : {};
    if (p === "/api/discovery/home") {
      const port = server.address().port;
      return json(res, 200, { id: HOME_ID, name: "Test home", version: "0.3.150", urls: [`http://127.0.0.1:${port}`], sign_in: { password: true, code: true }, api_level: 1, api_level_oldest: 1, client_floor: {} });
    }
    if (p === "/api/pairing/device" && req.method === "POST") return json(res, 201, { code: "HKMN-4567", device_code: state.deviceCode, expires_at: new Date(Date.now() + 6e5).toISOString(), verify_path: "/pair", interval: 1 });
    if (p === "/api/pairing/device/token" && req.method === "POST") {
      if (body.device_code === state.grant || body.device_code === state.deviceCode) {
        const t = mint();
        state.expired.add(t.access_token);
        return json(res, 200, t);
      }
      return json(res, 404, { type: "not_found" });
    }
    if (p === "/api/auth/refresh" && req.method === "POST") {
      if (!state.refresh.has(body.refresh_token)) return json(res, 401, { type: "unauthorized" });
      state.refresh.delete(body.refresh_token);
      const t = mint();
      return json(res, 200, { ...t, token_type: "Bearer", expires_in: 900, user: { id: "u-viewer", username: "viewer", is_admin: false } });
    }
    if (p.startsWith("/api/videos/") && p.endsWith("/poster")) {
      res.writeHead(200, { "Content-Type": "image/png" });
      return res.end(poster(p.split("/")[3]));
    }
    if (p.startsWith("/stream/")) {
      res.writeHead(404);
      return res.end();
    }
    if (!state.access.has(auth)) return json(res, 401, { type: "unauthorized" });
    if (p === "/api/home" && state.expired.has(auth)) {
      // the first call of a sign-in meets an expired token: the channel refreshes once
      state.expired.delete(auth);
      state.access.delete(auth);
      return json(res, 401, { type: "unauthorized" });
    }
    if (p === "/api/auth/me") return json(res, 200, { id: "u-viewer", username: "viewer", is_admin: false, home_name: "Test home" });
    if (p === "/api/home") return json(res, 200, { continue_watching: videos.filter((v) => v.position_sec > 0).slice(0, 8).map(card), recently_added: videos.slice(20, 32).map(card), on_deck: [], recently_watched: [], collection_rows: [] });
    if (p === "/api/recommendations") return json(res, 200, { rails: [{ id: "for-you", title: "For you", reason_kind: "for_you", items: videos.slice(40, 48).map((v) => ({ video_id: v.id, poster_url: `/api/videos/${v.id}/poster` })) }] });
    if (p === "/api/libraries") return json(res, 200, { items: [{ id: LIB, name: "Films and series" }, { id: uuid(901), name: "محاضرات" }] });
    if (p === `/api/libraries/${LIB}/hub`) return json(res, 200, { library: { id: LIB, name: "Films and series" }, rows: { continue_watching: videos.slice(0, 8).filter((v) => v.position_sec).map(card), recently_added: videos.slice(10, 22).map(card), on_deck: [], recently_watched: videos.slice(30, 38).map(card) } });
    if (p === "/api/videos/facets") return json(res, 200, { letters: [{ letter: "ر", videos: 3 }, { letter: "ح", videos: 2 }, { letter: "A", videos: 1 }, { letter: "N", videos: 2 }, { letter: "R", videos: 4 }, { letter: "T", videos: 4 }] });
    if (p === "/api/videos") {
      let list = videos.slice();
      const q = url.searchParams.get("q");
      if (q) list = list.filter((v) => v.title.toLowerCase().includes(q.toLowerCase()));
      const letter = url.searchParams.get("letter");
      if (letter) list = list.filter((v) => v.title.startsWith(letter) || v.title.replace(/^The /, "").startsWith(letter));
      if (url.searchParams.get("speaker")) list = list.slice(0, 7);
      const limit = Number(url.searchParams.get("limit") || 60);
      const start = Number(url.searchParams.get("cursor") || 0);
      const page = list.slice(start, start + limit);
      return json(res, 200, { items: page.map(card), next: start + limit < list.length ? String(start + limit) : null });
    }
    if (p.startsWith("/api/videos/")) {
      const v = videos.find((x) => x.id === p.split("/")[3]);
      if (!v) return json(res, 404, { type: "not_found" });
      return json(res, 200, { ...card(v), display_title: v.title, description: v.description, playable: true, playback_state: { position_sec: v.position_sec, completed: false } });
    }
    if (p === `/api/libraries/${LIB}/collections`) return json(res, 200, { items: [{ kind: "collection", id: uuid(700), name: "Journeys", videos: 6, poster_video_ids: [uuid(3)] }, { kind: "topic", id: uuid(701), name: "التاريخ", videos: 4, poster_video_ids: [uuid(8)] }] });
    if (p === `/api/libraries/${LIB}/categories`) return json(res, 200, { speakers: [{ value: "s1", label: "الشيخ أحمد", count: 7 }], topics: [{ value: "t1", label: "History", count: 4 }], languages: [{ value: "ar", label: "العربية", count: 40 }], years: [{ value: 2019, label: "2019", count: 9 }] });
    if (p === "/api/me/playlists") return json(res, 200, { items: [{ id: uuid(600), name: "Weekend", videos: 3, poster_video_ids: [uuid(5)] }] });
    if (p.startsWith("/api/me/playlists/")) return json(res, 200, { playlist: { id: uuid(600), name: "Weekend" }, items: videos.slice(4, 7).map(card) });
    if (p.startsWith("/api/collections/") || p.startsWith(`/api/libraries/${LIB}/collections/`)) return json(res, 200, { items: videos.slice(2, 8).map(card) });
    if (p === `/api/series/${SERIES}`) return json(res, 200, { id: SERIES, name: "حكايات البحر", description: "مسلسل عن الصيادين وسفنهم على ساحل المتوسط.", season_count: 2, episode_count: 12 });
    if (p === `/api/series/${SERIES}/episodes`) {
      const ep = (v, s, e) => ({ video_id: v.id, season: s, episode: e, title: v.title, duration_sec: v.duration_sec, progress_pct: v.position_sec ? 30 : 0, watched: false });
      return json(res, 200, { seasons: [{ season: 1, episodes: videos.slice(0, 6).map((v, i) => ep(v, 1, i + 1)) }, { season: 2, episodes: videos.slice(6, 12).map((v, i) => ep(v, 2, i + 1)) }], next_episode: null });
    }
    if (p === "/api/me/play-queues" && req.method === "POST") {
      log(`home queue source=${body.source?.kind} shuffle=${body.shuffle}`);
      return json(res, 201, { id: "q1", current_pos: 0, version: 1, items: videos.slice(0, 5).map((v, i) => ({ pos: i, id: v.id, title: v.title })) });
    }
    if (p === "/api/stream/sessions" && req.method === "POST") {
      log(`home session video=${body.video_id} profile=${body.client_profile} start=${body.start_sec ?? 0}`);
      if (!videos.find((x) => x.id === body.video_id)) return json(res, 404, { type: "not_found" });
      return json(res, 201, { session_id: "s1", mode: "direct", direct_url: `/stream/s1/direct.mp4?sig=x`, expires_at: new Date(Date.now() + 36e5).toISOString() });
    }
    return json(res, 404, { type: "not_found" });
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve({ server, state, port: server.address().port })));
}

/** Starts the cloud: a code, approved on the third poll with the home's connections. */
export function startCloud(log, homePort) {
  const state = { polls: 0, delivered: false, created: 0 };
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://x");
    const p = url.pathname;
    log(`cloud ${req.method} ${p}`);
    if (p === "/v1/tv/codes" && req.method === "POST") {
      const body = await readBody(req);
      state.created++;
      log(`cloud code for platform=${body.platform}`);
      return json(res, 201, { code: "ABCD-2345", poll_secret: "secret", expires_in: 600, interval: 1, verify_url: `http://127.0.0.1:${server.address().port}/tv?c=ABCD2345` });
    }
    if (p === "/v1/tv/codes/ABCD2345/qr.png") {
      res.writeHead(200, { "Content-Type": "image/png" });
      return res.end(qr());
    }
    if (p === "/v1/tv/codes/poll" && req.method === "POST") {
      await readBody(req);
      if (state.delivered) return json(res, 410, { error: "expired_token" });
      state.polls++;
      if (state.polls < 3) return json(res, 202, { status: "pending", interval: 1 });
      state.delivered = true;
      return json(res, 200, {
        status: "approved", home_id: HOME_ID, home_name: "Test home", grant: "G1",
        connections: [
          { kind: "local_ip", url: "https://127.0.0.1:8443", tls_name: "127-0-0-1.h1test.direct.example" },
          { kind: "local", url: `http://127.0.0.1:${homePort}` },
          { kind: "relay", url: "https://relay.example/h1test" },
        ],
      });
    }
    return json(res, 404, { error: "not_found" });
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve({ server, state, port: server.address().port })));
}
