// A fake of the saved-search API (web/test-cases/search.md) for the Search
// and matches-page tests. POST /api/search saves a snapshot under a new sid;
// GET /api/search/results/{sid} and /api/search/passages page over it; a sid
// in `expired` answers 410 search-expired, like the server after 15 minutes.
import type { Mock } from "vitest";
import { ApiError } from "../lib/api";
import { PROBLEM_SEARCH_EXPIRED } from "../pages/searchShared";

export interface FakePassage {
  segment_id: number;
  start_sec: number;
  end_sec: number;
  snippet: string | null;
  score: number;
}

/** A title match. */
export const titleVideo = (id: string, title: string, extra: Record<string, unknown> = {}) => ({
  video_id: id,
  title,
  duration_sec: 3725,
  library_id: "lib-1",
  library_name: "Lectures",
  poster_url: `/api/videos/${id}/poster`,
  score: 1,
  in_transcripts: false,
  ...extra,
});

export const passage = (
  id: number,
  start: number,
  snippet: string | null,
  end = start + 5.5
): FakePassage => ({ segment_id: id, start_sec: start, end_sec: end, snippet, score: 1 });

/** A transcript match: `all` are every matching passage, best first. */
export const transcriptVideo = (
  id: string,
  title: string,
  all: FakePassage[],
  extra: Record<string, unknown> = {}
) => ({
  ...titleVideo(id, title),
  best_score: 1,
  matches: all.length,
  passages: all.slice(0, 3),
  in_title: false,
  all,
  ...extra,
});

export type TitleVideoFx = ReturnType<typeof titleVideo>;
export type TranscriptVideoFx = ReturnType<typeof transcriptVideo>;

export interface Corpus {
  titles: TitleVideoFx[];
  transcripts: TranscriptVideoFx[];
}

export interface RecentFx {
  search_id: string;
  q: string;
  mode: string;
  created_at: string;
  last_used_at?: string;
  title_count: number;
  transcript_count: number;
  live: boolean;
  expires_at?: string;
}

export interface FakeServerOptions {
  pageSize?: number;
  passagesPageSize?: number;
  recent?: RecentFx[];
  saved?: unknown;
  ytAvailable?: boolean;
  /** Snapshots that exist before the test (a reload, a recent search). */
  existing?: Record<string, { q: string; mode: string; corpus: Corpus }>;
  /** Answers every other POST (save, import). */
  otherPost?: (url: string, body: unknown) => unknown;
}

export const expiredError = () =>
  new ApiError({ type: PROBLEM_SEARCH_EXPIRED, title: "search results expired", status: 410 });

/** Strips the fixture-only `all` from a transcript video. */
const wire = (v: TranscriptVideoFx) => {
  const rest: Partial<TranscriptVideoFx> = { ...v };
  delete rest.all;
  return rest;
};

/**
 * Installs the fake on the mocked api functions. `corpus` may depend on the
 * POST body (q, mode, view). Returns the server state for assertions.
 */
export function fakeSearchServer(
  mocks: { post: Mock; get: Mock; del: Mock },
  corpus: Corpus | ((body: Record<string, unknown>) => Corpus),
  opts: FakeServerOptions = {}
) {
  const pageSize = opts.pageSize ?? 20;
  const pps = opts.passagesPageSize ?? 20;
  const state = {
    n: 0,
    searches: new Map<string, { q: string; mode: string; corpus: Corpus }>(
      Object.entries(opts.existing ?? {})
    ),
    expired: new Set<string>(),
    recent: [...(opts.recent ?? [])],
  };
  mocks.post.mockImplementation(async (url: string, body: Record<string, unknown>) => {
    if (url === "/api/search") {
      const c = typeof corpus === "function" ? corpus(body) : corpus;
      const sid = `sid-${++state.n}`;
      state.searches.set(sid, { q: String(body.q), mode: String(body.mode), corpus: c });
      return {
        search_id: sid,
        expires_at: "2026-10-22T10:15:00Z",
        q: body.q,
        mode: body.mode,
        page_size: pageSize,
        passages_page_size: pps,
        videos: c.titles.slice(0, pageSize),
        videos_total: c.titles.length,
        transcript_videos: c.transcripts.slice(0, pageSize).map(wire),
        transcript_total: c.transcripts.length,
        hits: [],
        total: 0,
      };
    }
    return opts.otherPost?.(url, body);
  });
  mocks.get.mockImplementation(async (url: string) => {
    const u = new URL(url, "http://test");
    const p = u.searchParams;
    if (u.pathname === "/api/search/youtube/status")
      return { available: opts.ytAvailable === true };
    if (u.pathname === "/api/search/saved") return opts.saved ?? { items: [] };
    if (u.pathname === "/api/search/suggest") return { suggestions: [] };
    if (u.pathname === "/api/search/recent") return { items: state.recent };
    const res = /^\/api\/search\/results\/(.+)$/.exec(u.pathname);
    if (res) {
      const sid = decodeURIComponent(res[1]!);
      const s = state.searches.get(sid);
      if (!s || state.expired.has(sid)) throw expiredError();
      const list = p.get("list") === "titles" ? s.corpus.titles : s.corpus.transcripts.map(wire);
      const offset = Number(p.get("offset"));
      const limit = Number(p.get("limit"));
      return {
        search_id: sid,
        q: s.q,
        mode: s.mode,
        list: p.get("list"),
        offset,
        limit,
        total: list.length,
        items: list.slice(offset, offset + limit),
      };
    }
    if (u.pathname === "/api/search/passages") {
      const sid = p.get("sid")!;
      const s = state.searches.get(sid);
      if (!s || state.expired.has(sid)) throw expiredError();
      const vid = p.get("video_id")!;
      const tr = s.corpus.transcripts.find((v) => v.video_id === vid);
      const ti = s.corpus.titles.find((v) => v.video_id === vid);
      if (!tr && !ti) throw new ApiError({ type: "about:blank", title: "not found", status: 404 });
      const all = [...(tr?.all ?? [])];
      if (p.get("order") === "time") all.sort((a, b) => a.start_sec - b.start_sec);
      const offset = Number(p.get("offset"));
      const limit = Number(p.get("limit"));
      const card = tr ?? ti!;
      return {
        search_id: sid,
        q: s.q,
        mode: s.mode,
        video_id: vid,
        video: {
          video_id: vid,
          title: card.title,
          duration_sec: card.duration_sec,
          library_id: card.library_id,
          library_name: card.library_name,
          poster_url: card.poster_url,
        },
        in_title: Boolean(ti),
        title_snippet: ti ? `<mark>${ti.title}</mark>` : card.title,
        total: all.length,
        offset,
        limit,
        items: all.slice(offset, offset + limit),
        ...(p.get("include_times") === "1"
          ? { times: all.map((x) => x.start_sec).sort((a, b) => a - b) }
          : {}),
      };
    }
    if (u.pathname.startsWith("/api/videos/") && u.pathname.endsWith("/chapters")) {
      return { items: [] };
    }
    throw new Error(`unmocked GET ${url}`);
  });
  mocks.del.mockImplementation(async (url: string) => {
    if (url === "/api/search/recent") state.recent = [];
    const m = /^\/api\/search\/recent\/(.+)$/.exec(url);
    if (m) state.recent = state.recent.filter((r) => r.search_id !== decodeURIComponent(m[1]!));
    return undefined;
  });
  return state;
}

/** n title matches named "Title 1".."Title n". */
export const manyTitles = (n: number, prefix = "Title") =>
  Array.from({ length: n }, (_, i) => titleVideo(`t${i + 1}`, `${prefix} ${i + 1}`));

/** n transcript matches, each with `per` passages. */
export const manyTranscripts = (n: number, per = 1, prefix = "Talk") =>
  Array.from({ length: n }, (_, i) =>
    transcriptVideo(
      `r${i + 1}`,
      `${prefix} ${i + 1}`,
      Array.from({ length: per }, (_, j) =>
        passage((i + 1) * 1000 + j, 60 * (j + 1), `the <mark>fox</mark> ${i + 1}.${j + 1}`)
      )
    )
  );
