// The demo library (tests/fixtures/demo-library, story 60.09) as the channel's mock home answers
// it, for the store gallery's Roku screens (story 60.14 part 2): mockhome.mjs signs the channel
// in and keeps its parental answers; this answers everything the library holds, in the language
// `lang` names (set it before the channel reloads a screen), from the fixture's own route table.
//
// The fixture is not edited. What the channel needs that it lacks is made here, from it:
//   - ids: the channel opens a deep link only with a UUID (source/deeplink.brs), so every id the
//     library names (videos, series, libraries, channels) is answered as a UUID and mapped back;
//   - a video's `series` ({id, name}) on its cards, as a home sends it, so * on an episode's card
//     offers Go to the series;
//   - the stream session (direct play) and its sidecar subtitles, the lines Apple's demo player
//     shows (apps/apple/UITests/Fixtures/Stream/demo-subs-*.vtt), Arabic the library's default;
//   - the subtitle list in the shape the channel reads (`language`, `is_default`).

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { ROKU } from "../brs.mjs";

const REPO = join(ROKU, "..", "..", "..");
const FIXTURE = join(REPO, "tests", "fixtures", "demo-library");
const { buildRoutes, loadLibrary } = await import(join(FIXTURE, "routes.mjs"));

const STREAM = join(REPO, "apps", "apple", "UITests", "Fixtures", "Stream");

/** The sidecar subtitle a language shows: the Apple demo's line, without its HLS timestamp map. */
function vtt(lang) {
  return readFileSync(join(STREAM, `demo-subs-${lang}.vtt`), "utf8").replace(/^X-TIMESTAMP-MAP=.*\n/m, "");
}

export function demoHome() {
  const lib = loadLibrary();
  // every id the library names, as a UUID (longest first, so none is cut out of a longer one)
  const ids = [...lib.videos.map((v) => v.id), ...lib.series.map((s) => s.id), ...lib.libraries.map((l) => l.id), ...lib.channels.map((c) => c.id)];
  const toUuid = new Map(ids.map((id, i) => [id, `00000000-0000-4000-8000-${String(600 + i).padStart(12, "0")}`]));
  const toId = new Map([...toUuid].map(([a, b]) => [b, a]));
  const order = [...toUuid.keys()].sort((a, b) => b.length - a.length);
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const idRe = new RegExp(`(?<![\\w-])(${order.map(esc).join("|")})(?![\\w-])`, "g");
  const uuidRe = /[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}/g;
  const outward = (text) => text.replace(idRe, (id) => toUuid.get(id));
  const inward = (path) => path.replace(uuidRe, (u) => toId.get(u) ?? u);

  const tables = {};
  const table = (lang) => {
    if (!tables[lang]) {
      const exact = new Map();
      const patterns = [];
      for (const r of buildRoutes(lang, lib)) {
        if (r.path.includes("{")) patterns.push([r.method, new RegExp(`^${r.path.replace(/\{[^}]+\}/g, "[^/]+")}$`), r]);
        else exact.set(`${r.method} ${r.path}`, r);
      }
      tables[lang] = (method, path) => exact.get(`${method} ${path}`) ?? patterns.find(([m, re]) => m === method && re.test(path))?.[2];
    }
    return tables[lang];
  };

  const byId = new Map(lib.videos.map((v) => [v.id, v]));
  const seriesOf = (lang, v) => {
    const s = lib.series.find((x) => x.id === v.series_id);
    return s ? { id: s.id, name: s.name[lang] } : null;
  };
  // a video's card or record (`id` a video's) carries its series, as a home's does
  const withSeries = (lang, x) => {
    if (Array.isArray(x)) return x.map((y) => withSeries(lang, y));
    if (!x || typeof x !== "object") return x;
    const out = Object.fromEntries(Object.entries(x).map(([k, v]) => [k, withSeries(lang, v)]));
    const v = byId.get(out.id);
    if (v && out.series === undefined) {
      const s = seriesOf(lang, v);
      if (s) out.series = s;
    }
    return out;
  };
  const send = (res, status, body) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(outward(JSON.stringify(body)));
  };

  const demo = {
    lang: "en",
    name: lib.home.en,
    names: lib.home,
    sessions: 0,
    /** A library id as the channel sees it. */
    uuid: (id) => toUuid.get(id),
    /** The library's search query in a language. */
    query: (lang) => lib.search.query[lang],
    /** Art and the sidecar subtitles: a Roku's Poster and its subtitle fetch send no bearer. */
    async open(req, res, p) {
      const path = inward(p);
      const sub = /^\/stream\/(s\d+)\/subs\/sidecar\/(en|ar)\.vtt$/.exec(path);
      if (sub) {
        res.writeHead(200, { "Content-Type": "text/vtt" });
        res.end(vtt(sub[2]));
        return true;
      }
      if (req.method !== "GET" || !path.startsWith("/api/")) return false;
      const r = table(demo.lang)("GET", path);
      if (!r?.art) return false;
      res.writeHead(200, { "Content-Type": "image/jpeg" });
      res.end(readFileSync(join(FIXTURE, "art", r.art)));
      return true;
    },
    /** The library's answers, after the bearer check. */
    async answer(req, res, p, url, body) {
      const path = inward(p);
      const lang = demo.lang;
      if (path === "/api/stream/sessions" && req.method === "POST") {
        const id = toId.get(body.video_id) ?? body.video_id;
        const v = byId.get(id);
        if (!v) return send(res, 404, { type: "not_found" }), true;
        demo.sessions++;
        const sid = `s${demo.sessions}`;
        send(res, 201, { session_id: sid, mode: "direct", direct_url: `/stream/direct/${v.id}?sig=demo`, stream_sig: `sig-${sid}`, duration_sec: v.duration_sec, expires_at: new Date(Date.now() + 36e5).toISOString() });
        return true;
      }
      if (/^\/api\/stream\/sessions\/s\d+(\/progress)?$/.test(path)) {
        res.writeHead(204);
        res.end();
        return true;
      }
      // no trickplay sheet in the library
      if (/^\/api\/videos\/[^/]+\/trickplay$/.test(path)) return send(res, 404, { type: "not_found" }), true;
      if (/^\/api\/videos\/[^/]+\/subtitles$/.test(path)) {
        const items = ["ar", "en"].map((l, i) => ({ id: i + 1, language: l, format: "vtt", source: "sidecar", is_default: l === "ar", url: "" }));
        return send(res, 200, { items }), true;
      }
      const r = table(lang)(req.method, path);
      if (!r || r.art) return false;
      if (r.status === 204) {
        res.writeHead(204);
        res.end();
        return true;
      }
      // the channel shows a viewer's screens
      let answer = withSeries(lang, r.body);
      if (path === "/api/auth/me" || path === "/api/me") answer = { ...answer, is_admin: false, ...(answer.user ? { user: { ...answer.user, is_admin: false } } : {}) };
      // times as a home sends them: from now (the channel reads none it draws)
      send(res, r.status, JSON.parse(JSON.stringify(answer).replace(/\{\{now([+-]\d+)?\}\}/g, (_, off) => new Date(Date.now() + Number(off ?? 0) * 1000).toISOString())));
      return true;
    },
  };
  return demo;
}
