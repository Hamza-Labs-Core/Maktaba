// A mock Maktaba home and Maktaba Cloud for the channel's screen tests on brs-engine (story
// 58.12 T03). Test data only: two libraries, English and Arabic titles, a series with two
// seasons, collections, a playlist, categories, the letter facets, and posters drawn here.
//
// The home answers as the real one does where the channel reads it (field names from the API's
// handlers and OpenAPI): bearer tokens on every route but discovery, pairing and refresh; a 401
// for an expired access token (the first /api/home of each sign-in, to run the refresh); 404
// for a video the viewer cannot see. The cloud gives a code and approves it on the third poll.
// Given a certificate for localhost, the home also answers over https on localhost: the name the
// mock cloud gives it as its relay, the only kind of connection the cloud code's grant may go to.
// Epic 57 on Roku (story 58.14): with `state.time` set, a restricted viewer's watching time is
// over, and every stream open and progress post answers 403 parental_time (the problem body the
// home's streaming handler writes) until a parent approves a request for more time; a video in
// `state.hiddenNow` is hidden by the viewer's limit from then on (404 for it, its stream's
// progress included) until a parent approves a request for it. GET /api/me/parental/time answers
// as the home's handler does: `state.timeLeft` seconds left under a rule (null: no rule now),
// not allowed while `state.time` holds.
// Every request is logged (no tokens) for the CI report.

import { createServer } from "node:http";
import { createServer as createTlsServer } from "node:https";
import { createServer as createTcpServer } from "node:net";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { createCanvas } = require("canvas");

const uuid = (n) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
// The home's instance id, as /api/discovery/home answers it, and its server id in Maktaba
// Cloud, as the cloud's poll answers it (home_id): two different ids, as on a real home, so the
// channel cannot know the approving home by an id; it trusts only an https name (its relay).
export const HOME_ID = "h1test";
export const CLOUD_HOME_ID = "7c9e6679-7425-40de-944b-e07fc1f90ae7";
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

/**
 * Story 58.14: a 16+ video. With `state.restricted` the home holds the viewer to 9+ and answers
 * 404 for it (as for a missing one) until a parent approves the viewer's request for it; Home's
 * Continue watching still lists it first (a limit tightened since the row was read).
 */
export const LATE = uuid(998);
const late = { id: LATE, title: "Late Night Film", filename: "late.mkv", library_id: LIB, duration_sec: 5400, position_sec: 900, completed: false, series: null, description: "A film for grown-ups.", playable: true };

/** A video whose stream session the home refuses (every transcode slot taken). */
export const REFUSED = uuid(8);

/** The sidecar subtitles: an Arabic cue the channel draws itself, an English one for Roku's. */
const VTT = {
  ar: "WEBVTT\n\n00:00:00.000 --> 01:00:00.000\nمرحباً بكم في رحلة إلى الأندلس\nالسفينة تبحر عند الفجر\n",
  en: "WEBVTT\n\n00:00:00.000 --> 01:00:00.000\nWelcome aboard.\n",
};

/** A trickplay sheet: 5×5 tiles of 320×180, each numbered. */
function sprite() {
  const c = createCanvas(1600, 900);
  const g = c.getContext("2d");
  for (let i = 0; i < 25; i++) {
    const x = (i % 5) * 320;
    const y = Math.floor(i / 5) * 180;
    g.fillStyle = colours[i % colours.length];
    g.fillRect(x, y, 320, 180);
    g.fillStyle = "#E8ECF3";
    g.font = "bold 48px sans-serif";
    g.fillText(`${i * 80}s`, x + 20, y + 100);
  }
  return c.toBuffer("image/png");
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
export function startHome(log, tls) {
  const state = { access: new Set(), refresh: new Set(), expired: new Set(), n: 0, grant: "G1", deviceCode: "dc1", clientHeaders: new Set(), restricted: false, requests: [] };
  // story 58.14: the late video is the viewer's to see once not restricted, or once approved
  const lateShown = () => !state.restricted || state.requests.some((r) => r.subject_id === LATE && r.status === "approved");
  state.time = null;
  state.timeLeft = null;
  state.hiddenNow = new Set();
  state.sessionVideo = {};
  // a parent's yes to more time gives time: the home admits the stream again
  const timeOver = () => state.restricted && state.time && !state.requests.some((r) => r.subject_kind === "time" && r.status === "approved");
  const hiddenNow = (id) => state.restricted && state.hiddenNow.has(id) && !state.requests.some((r) => r.subject_id === id && r.status === "approved");
  const notFound = (p) => ({ type: "not-found", title: "Not Found", status: 404, detail: "not found", instance: p, requestId: "req-test" });
  // streaming.writeTimeOver: 403 parental-time with its code, reason and, when the rules allow
  // watching again, next_allowed_at
  const timeProblem = (p) => ({ type: "parental-time", title: "Forbidden", status: 403, detail: "this account's watching time is over for now", instance: p, requestId: "req-test", code: "parental_time", reason: state.time.reason, ...(state.time.next ? { next_allowed_at: state.time.next } : {}) });
  const card = (v) => ({ id: v.id, title: v.title, filename: v.filename, library_id: v.library_id, duration_sec: v.duration_sec, position_sec: v.position_sec, completed: v.completed, series: v.series });
  const mint = () => {
    state.n++;
    const a = `A${state.n}`;
    const r = `R${state.n}`;
    state.access.add(a);
    state.refresh.add(r);
    return { access_token: a, refresh_token: r, access_expires_in: 900, refresh_expires_in: 2592000, user_id: "u-viewer" };
  };
  const handler = async (req, res) => {
    const url = new URL(req.url, "http://x");
    const p = url.pathname;
    const auth = (req.headers.authorization || "").replace(/^Bearer /, "");
    state.clientHeaders.add(req.headers["x-maktaba-client"] || "");
    log(`home ${req.method} ${p}${url.search ? " ?" + [...url.searchParams.keys()].join(",") : ""} auth=${auth ? "bearer" : "none"}`);
    const body = req.method === "POST" || req.method === "PUT" ? await readBody(req) : {};
    if (p === "/api/discovery/home") {
      const port = server.address().port;
      return json(res, 200, { id: HOME_ID, name: "Test home", version: "0.3.150", urls: [`http://127.0.0.1:${port}`, ...(secure ? [`https://localhost:${secure.address().port}`] : [])], sign_in: { password: true, code: true }, api_level: 1, api_level_oldest: 1, client_floor: {} });
    }
    if (p === "/api/pairing/device" && req.method === "POST") return json(res, 201, { code: "HKMN-4567", device_code: state.deviceCode, expires_at: new Date(Date.now() + 6e5).toISOString(), verify_path: "/pair?code=HKMN-4567", verify_url: `http://127.0.0.1:${server.address().port}/pair?code=HKMN-4567`, interval: 1 });
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
    if (p.startsWith("/api/videos/") && p.endsWith("/trickplay/sprite")) {
      // like the poster: brs-engine's Poster sends no bearer
      res.writeHead(200, { "Content-Type": "image/png" });
      return res.end(sprite());
    }
    if (p.startsWith("/stream/")) {
      // a session's sidecar subtitles, signed with its stream_sig (Roku sends no header for media)
      const sub = /^\/stream\/(s\d+)\/subs\/sidecar\/(\w+)\.vtt$/.exec(p);
      if (sub && url.searchParams.get("sig") === `sig-${sub[1]}` && VTT[sub[2]]) {
        log(`home subtitles ${sub[2]} signed`);
        res.writeHead(200, { "Content-Type": "text/vtt" });
        return res.end(VTT[sub[2]]);
      }
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
    if (p === "/api/me/parental") return json(res, 200, state.restricted ? { restricted: true, max_tier: 9, allow_only: false, version: 1 } : { restricted: false });
    if (p === "/api/me/parental/requests" && req.method === "POST") {
      log(`home ask kind=${body.subject_kind} id=${body.subject_id}`);
      const r = { id: `r${state.requests.length + 1}`, subject_kind: body.subject_kind, subject_id: body.subject_id, status: "pending", created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 864e5).toISOString() };
      state.requests.unshift(r);
      return json(res, 201, { request: r });
    }
    if (p === "/api/me/parental/requests") return json(res, 200, { requests: state.requests });
    if (p === "/api/me/parental/time") {
      // handlers/parental/time.go's myTimeResponse; it counts nothing and renews no lease
      log(`home time library=${url.searchParams.get("library_id") ?? ""} left=${state.timeLeft}`);
      const now = new Date().toISOString();
      if (timeOver()) return json(res, 200, { allowed: false, limited: true, reason: state.time.reason, remaining_seconds: 0, slot_ends_at: null, next_allowed_at: state.time.next ?? null, now });
      if (state.timeLeft === null) return json(res, 200, { allowed: true, limited: false, remaining_seconds: null, slot_ends_at: null, next_allowed_at: null, now });
      return json(res, 200, { allowed: true, limited: true, remaining_seconds: state.timeLeft, slot_ends_at: null, next_allowed_at: null, now });
    }
    if (p.startsWith("/api/videos/") && hiddenNow(p.split("/")[3])) return json(res, 404, notFound(p));
    if (p === `/api/videos/${LATE}`) {
      if (!lateShown()) return json(res, 404, { type: "about:blank", title: "Not Found", status: 404, detail: "not found" });
      return json(res, 200, { ...card(late), display_title: late.title, description: late.description, playable: true, playback_state: { position_sec: late.position_sec, completed: false } });
    }
    if (p === "/api/home" && state.restricted)
      return json(res, 200, { continue_watching: [late, ...videos.filter((v) => v.position_sec > 0).slice(0, 7)].map(card), recently_added: videos.slice(20, 32).map(card), on_deck: [], recently_watched: [], collection_rows: [] });
    if (p === "/api/home") return json(res, 200, { continue_watching: videos.filter((v) => v.position_sec > 0).slice(0, 8).map(card), recently_added: videos.slice(20, 32).map(card), on_deck: [], recently_watched: [], collection_rows: [] });
    if (p === "/api/recommendations") return json(res, 200, { rails: [{ id: "for-you", title: "For you", reason_kind: "for_you", items: videos.slice(40, 48).map((v) => ({ video_id: v.id, poster_url: `/api/videos/${v.id}/poster` })) }] });
    if (p === "/api/libraries") return json(res, 200, { items: [{ id: LIB, name: "Films and series" }, { id: uuid(901), name: "محاضرات" }] });
    if (p === `/api/libraries/${LIB}/hub`) return json(res, 200, { library: { id: LIB, name: "Films and series" }, rows: { continue_watching: videos.slice(0, 8).filter((v) => v.position_sec).map(card), recently_added: videos.slice(10, 22).map(card), on_deck: [], recently_watched: videos.slice(30, 38).map(card) } });
    if (p === "/api/videos/facets") return json(res, 200, { letters: [{ letter: "ر", videos: 3 }, { letter: "ح", videos: 2 }, { letter: "A", videos: 1 }, { letter: "N", videos: 2 }, { letter: "R", videos: 4 }, { letter: "T", videos: 4 }] });
    // story 49.11: search with story 49.8's help: suggestions with their kinds, did-you-mean
    // for "harbor", the order, and facets counted over the videos found
    if (p === "/api/search/suggest") {
      const river = videos.find((v) => v.title === "River of Stars");
      return json(res, 200, {
        suggestions: ["river", "harbor", "River of Stars", "Rania"],
        items: [
          { text: "river", kind: "history" },
          { text: "harbor", kind: "term" },
          { text: "River of Stars", kind: "title", video_id: river.id },
          { text: "Rania", kind: "speaker" },
        ],
      });
    }
    // story 49.14: AI mode (the API of story 49.12) as Server-Sent Events: the line, the
    // passages, two cited sentences, done; a follow-up (history) answers the follow-up;
    // state.ai "unavailable" answers 409 ai-unavailable, "off" 409 ai-off, "busy" 429,
    // "noanswer" the passages and answered false; state.aiLang "ar" answers in Arabic
    if (p === "/api/search/ai" && req.method === "POST") {
      log(`home ai ${JSON.stringify(body)}`);
      const mode = state.ai || "ok";
      const problem = (status, code, extra = {}) => json(res, status, { type: code, title: code, status, detail: code, instance: p, requestId: "req-test", code, ...extra });
      if (mode === "unavailable") return problem(409, "ai-unavailable", { help: { settings: "/settings/rules/tags", help: "/help/search#ai-mode" } });
      if (mode === "off") return problem(409, "ai-off");
      if (mode === "busy") {
        res.setHeader("Retry-After", "30");
        return problem(429, "ai-busy", { position: 9 });
      }
      const ar = state.aiLang === "ar";
      const pick = [videos[3], videos[7], videos[12]];
      const words = ar ? ["الصبر ثلاثة أنواع", "الشكر نصف الإيمان", "الأمل"] : ["Patience is of three kinds.", "Gratitude goes with patience.", "Hope at dawn."];
      const passages = pick.map((v, i) => ({ n: i + 1, video_id: v.id, title: v.title, library_id: LIB, library_name: "Films and series", poster_url: `/api/videos/${v.id}/poster`, segment_id: i + 1, start_sec: 60 * (i + 1) + 15, end_sec: 60 * (i + 1) + 25, text: words[i] }));
      const cite = (n) => ({ n, video_id: passages[n - 1].video_id, start_sec: passages[n - 1].start_sec, end_sec: passages[n - 1].end_sec, quote: passages[n - 1].text });
      const follow = Array.isArray(body.history) && body.history.length > 0;
      const sentences = ar
        ? follow
          ? [{ text: "والشكر يصحب الصبر.", citations: [cite(2)] }]
          : [{ text: "للصبر ثلاثة أنواع.", citations: [cite(1)] }, { text: "والشكر نصف الإيمان.", citations: [cite(2), cite(1)] }]
        : follow
          ? [{ text: "Gratitude goes with it.", citations: [cite(2)] }]
          : [{ text: "Patience is of three kinds.", citations: [cite(1)] }, { text: "Gratitude goes with patience.", citations: [cite(2), cite(1)] }];
      res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
      const send = (event, data) => res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
      const pause = (ms) => new Promise((r) => setTimeout(r, ms));
      res.write(": ping\n\n");
      send("queue", { position: 1 });
      await pause(100);
      send("sources", { q: body.q, passages });
      await pause(100);
      if (mode === "noanswer") {
        send("done", { answered: false, reason: "not-in-passages", sentences: 0, dropped: 1, took_ms: 300 });
        return res.end();
      }
      for (const t of sentences) {
        send("text", t);
        await pause(50);
      }
      send("done", { answered: true, sentences: sentences.length, dropped: 0, first_token_ms: 120, took_ms: 400, model: "test" });
      return res.end();
    }
    if (p === "/api/search" && req.method === "POST") {
      log(`home search ${JSON.stringify(body)}`);
      const q = String(body.q || "").toLowerCase();
      const lang = (v) => (/[\u0600-\u06ff]/.test(v.title) ? "ar" : "en");
      const f = body.filters || {};
      let list = videos.filter((v) => v.title.toLowerCase().includes(q));
      if (f.language) list = list.filter((v) => f.language.includes(lang(v)));
      if (f.duration_sec?.gte >= 3600) list = list.filter((v) => v.duration_sec >= 3600);
      if (body.sort === "newest") list = list.slice().reverse();
      const count = (pred) => list.filter(pred).length;
      const facets = {
        library: list.length ? [{ value: LIB, label: "Films and series", count: list.length }] : [],
        language: ["ar", "en"].map((l) => ({ value: l, count: count((v) => lang(v) === l) })).filter((b) => b.count),
        speaker: list.length ? [{ value: "Rania", count: Math.min(2, list.length) }] : [],
        year: list.length ? [{ value: "2024", count: list.length, filter: { date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" } } }] : [],
        duration: [
          { value: "20m_60m", count: count((v) => v.duration_sec < 3600) },
          { value: "over_60m", count: count((v) => v.duration_sec >= 3600) },
        ].filter((b) => b.count),
        videos: list.length,
      };
      return json(res, 200, {
        search_id: "s1",
        q: body.q,
        mode: "hybrid",
        sort: body.sort || "relevance",
        videos: list.slice(0, 24).map((v) => ({ video_id: v.id, title: v.title, duration_sec: v.duration_sec, library_id: v.library_id })),
        videos_total: list.length,
        transcript_videos: [],
        transcript_total: 0,
        facets,
        ...(q === "harbor" ? { did_you_mean: "harbour" } : {}),
      });
    }
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
    if (p.startsWith("/api/videos/") && p.endsWith("/subtitles")) {
      return json(res, 200, { items: [{ id: 1, language: "en", format: "vtt", source: "sidecar", is_default: false, url: "" }, { id: 2, language: "ar", format: "vtt", source: "sidecar", is_default: false, url: "" }] });
    }
    if (p.startsWith("/api/videos/") && p.endsWith("/chapters")) {
      return json(res, 200, { items: [{ id: 1, seq: 1, start_sec: 0, end_sec: 600, title: "البداية", source: "auto" }, { id: 2, seq: 2, start_sec: 600, title: "The harbour", source: "auto" }] });
    }
    if (p.startsWith("/api/videos/") && p.endsWith("/trickplay")) {
      return json(res, 200, { sprite_url: `${p}/sprite`, columns: 5, rows: 5, count: 25, interval_sec: 80, tile_width: 320, tile_height: 180, sheet_width: 1600, sheet_height: 900 });
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
    if (p.startsWith("/api/me/play-queues/") && p.endsWith("/current") && req.method === "PUT") {
      log(`home queue move pos=${body.pos} version=${body.version}`);
      return json(res, 200, { id: "q1", current_pos: body.pos, version: (body.version || 1) + 1, items: videos.slice(0, 5).map((v, i) => ({ pos: i, id: v.id, title: v.title })) });
    }
    if (p === "/api/stream/sessions" && req.method === "POST") {
      const caps = body.device_caps || {};
      log(`home session video=${body.video_id} profile=${body.client_profile} start=${body.start_sec ?? 0} caps=${(caps.video || []).map((v) => `${v.codec}@${v.max_height}`).join(",")}/${(caps.hdr || []).join(",")}/${(caps.passthrough || []).join(",")} force=${body.force_transcode === true}`);
      if (timeOver()) {
        log("home time over");
        return json(res, 403, timeProblem(p));
      }
      const v = videos.find((x) => x.id === body.video_id) || (body.video_id === LATE && lateShown() ? late : null);
      if (!v || hiddenNow(v.id)) return json(res, 404, notFound(p));
      if (v.id === REFUSED) return json(res, 503, { type: "about:blank", title: "Service Unavailable", status: 503, detail: "Every transcode slot is taken." });
      state.sessions = (state.sessions || 0) + 1;
      const id = `s${state.sessions}`;
      state.sessionVideo[id] = v.id;
      // the first video of the queue is short: its Up next shows at once (15 s before the end)
      const duration = v.id === videos[0].id ? 12 : v.duration_sec;
      return json(res, 201, { session_id: id, mode: "direct", direct_url: `/stream/direct/${v.id}?sig=x`, stream_sig: `sig-${id}`, duration_sec: duration, expires_at: new Date(Date.now() + 36e5).toISOString() });
    }
    if (/^\/api\/stream\/sessions\/s\d+\/progress$/.test(p) && req.method === "POST") {
      const sid = p.split("/")[4];
      log(`home progress ${sid} pos=${body.position_sec} completed=${body.completed === true}`);
      // the heartbeat of a kept account: refused once the time is over (the position is kept)
      if (timeOver()) {
        log("home time over");
        return json(res, 403, timeProblem(p));
      }
      // the stream's video is no longer this viewer's
      if (hiddenNow(state.sessionVideo[sid])) return json(res, 404, notFound(p));
      res.writeHead(204);
      return res.end();
    }
    if (/^\/api\/stream\/sessions\/s\d+$/.test(p) && req.method === "DELETE") {
      log(`home close ${p.split("/")[4]}`);
      res.writeHead(204);
      return res.end();
    }
    return json(res, 404, { type: "not_found" });
  };
  const server = createServer(handler);
  const secure = tls ? createTlsServer(tls, handler) : null;
  if (secure) server.on("close", () => secure.close());
  const listen = (s) => new Promise((resolve) => s.listen(0, "127.0.0.1", resolve));
  return Promise.all([listen(server), ...(secure ? [listen(secure)] : [])]).then(() => ({ server, state, port: server.address().port, httpsPort: secure ? secure.address().port : 0 }));
}

/**
 * Starts the cloud: a code, approved on the third poll with the home's connections. The relay is
 * the home's https name (localhost, homeHttpsPort). The LAN address is a trap: another machine
 * answering as a Maktaba home there and listing the home's relay among its urls, as anyone on
 * the TV's network could. A raw-IP direct address (https and plain) leads to a second trap. The
 * channel must never contact either: state.trapConnections counts every TCP connection to them.
 */
export async function startCloud(log, homeHttpsPort) {
  const state = { polls: 0, delivered: false, created: 0, trapConnections: 0 };
  const relay = `https://localhost:${homeHttpsPort}`;
  const lanTrap = createServer((req, res) => {
    log(`trap ${req.method} ${new URL(req.url, "http://x").pathname}`);
    json(res, 200, { id: "intruder", name: "Not the home", version: "0.3.150", urls: [relay], sign_in: { password: true, code: true }, api_level: 1, api_level_oldest: 1, client_floor: {} });
  });
  const ipTrap = createTcpServer((sock) => sock.destroy());
  for (const t of [lanTrap, ipTrap]) {
    t.on("connection", () => {
      state.trapConnections++;
      log("trap connection");
    });
    await new Promise((resolve) => t.listen(0, "127.0.0.1", resolve));
  }
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, "http://x");
    const p = url.pathname;
    log(`cloud ${req.method} ${p}`);
    if (p === "/v1/tv/codes" && req.method === "POST") {
      const body = await readBody(req);
      state.created++;
      log(`cloud code for platform=${body.platform}`);
      return json(res, 201, { code: "ABCD-2345", poll_secret: "secret", expires_in: 600, interval: 1, verify_url: "https://maktaba.tv/tv?c=ABCD2345" });
    }
    if (p === "/v1/tv/codes/poll" && req.method === "POST") {
      await readBody(req);
      if (state.delivered) return json(res, 410, { error: "expired_token" });
      state.polls++;
      if (state.polls < 3) return json(res, 202, { status: "pending", interval: 1 });
      state.delivered = true;
      return json(res, 200, {
        status: "approved", home_id: CLOUD_HOME_ID, home_name: "Test home", grant: "G1",
        connections: [
          { kind: "local_ip", url: "https://127.0.0.1:8443", tls_name: "127-0-0-1.h1test.direct.example" },
          { kind: "local", url: `http://127.0.0.1:${lanTrap.address().port}` },
          { kind: "direct", url: `https://127.0.0.1:${ipTrap.address().port}` },
          { kind: "direct", url: `http://127.0.0.1:${ipTrap.address().port}` },
          { kind: "relay", url: relay },
        ],
      });
    }
    return json(res, 404, { error: "not_found" });
  });
  server.on("close", () => {
    lanTrap.close();
    ipTrap.close();
  });
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve({ server, state, port: server.address().port })));
}
