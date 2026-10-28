// Story 11.4 — Search interface (results redesign: two paged lists over a
// saved search).
//
// Real server contract (api/internal/handlers/search):
//   POST   /api/search          { q, mode, view } -> { search_id, page_size,
//          videos (In titles, page 1), videos_total, transcript_videos
//          (In transcripts, one row per video, page 1), transcript_total, ... }
//   GET    /api/search/results/{sid}?list=titles|transcripts&offset=&limit=
//   GET    /api/search/passages?sid=&video_id=&order=score&offset=&limit=
//   GET    /api/search/recent   DELETE /api/search/recent[/{sid}]
//   GET    /api/search/suggest?q=  -> { suggestions: string[] }
//   POST   /api/search/save     { q, mode }
//   GET    /api/search/saved    -> { items:[{ id, q, mode }] }
//   DELETE /api/search/saved/{id}
//   GET    /api/search/youtube/status -> { available }   (yt-dlp installed?)
//   GET    /api/search/youtube?q=     -> { items, reason? }
//   POST   /api/videos/{id}/import-youtube  { youtube_id, title, channel, ... }
//
// URL state: /search?q=&mode=&sid=&vp=<titles page>&tp=<transcripts page>.
// With a sid the pages are read from the saved search (Back / Forward /
// reload keep the same order); without one the search is POSTed and its sid
// replaces the URL. A sid read answering 410 search-expired runs the search
// again, keeps the pages (clamped) and says the order may have changed —
// pages of two sids are never shown together. Snippet `<mark>` runs are
// rendered as escaped React nodes (no raw HTML).
import { type FormEvent, type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Modal } from "@ds/components/Modal/Modal";
import { Pagination } from "@ds/components/Pagination/Pagination";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { useAuth } from "../lib/auth";
import { PageHelp } from "../help/HelpLink";
import {
  type View,
  VideoPoster,
  ViewToggle,
  formatDuration,
  useStoredView,
} from "../components/VideoViews";
import {
  DEFAULT_PASSAGES_PAGE_SIZE,
  MODE_KEY,
  type Mode,
  type Passage,
  type PassagesPage,
  type ResultsPage,
  type SearchResponse,
  type SearchState,
  type TitleVideo,
  type TranscriptVideo,
  type VideoCard,
  asMode,
  defaultPageSize,
  isSearchExpired,
  listOf,
  matchesHref,
  matchesLabel,
  pageCount,
  pageParam,
  readMode,
  recallSizes,
  rememberSizes,
  renderSnippet,
  searchParamsOf,
  ts,
  urlMode,
  watchHref,
} from "./searchShared";
import { errorText } from "../lib/errorText";
import { cachedAnswer, keepAnswer } from "../lib/pageCache";

const VIEW_KEY = "mkt:search:view";

type ListKind = "titles" | "transcripts";

interface YTItem {
  youtube_id: string;
  title: string;
  channel: string;
  description_snippet: string;
  thumbnail: string;
  published_at?: string;
  duration_sec?: number;
  view_count?: number;
  match?: { state: string; video_id?: string } | null;
}
interface YTBlock {
  items: YTItem[];
  reason?: string;
}
interface SavedSearch {
  id: string;
  q: string;
  mode: string;
}
interface RecentSearch {
  search_id: string;
  q: string;
  mode: string;
  created_at: string;
  last_used_at?: string;
  title_count?: number;
  transcript_count?: number;
  live: boolean;
  expires_at?: string;
}
/** The saved search on screen. */
interface Snapshot {
  sid: string;
  q: string;
  mode: Mode;
  pageSize: number;
  passagesPageSize: number;
  titlesTotal: number;
  transcriptsTotal: number;
}
/** The pages on screen of a saved search, kept for a return (lib/pageCache). */
interface KeptSearch {
  s: Snapshot;
  vPage: number;
  vItems: TitleVideo[];
  tPage: number;
  tItems: TranscriptVideo[];
}
const keptKey = (sid: string, vp: number, tp: number) => `search:${sid}:${vp}:${tp}`;

/** One page of a list, and the saved search it came from. */
interface Loaded<T> {
  sid: string;
  page: number;
  items: T[];
}

const YT_REASONS = new Set(["disabled", "rate_limited", "error"]);
const runKey = (q: string, m: Mode, sid: string) => `${q}\n${m}\n${sid}`;
const total = (v: unknown, atLeast: number) =>
  typeof v === "number" && Number.isFinite(v) ? Math.max(v, atLeast) : atLeast;

function fetchPage<T>(list: ListKind, sid: string, page: number, size: number) {
  return api.get<ResultsPage<T>>(
    `/api/search/results/${encodeURIComponent(sid)}?list=${list}&offset=${(page - 1) * size}&limit=${size}`
  );
}

/** "5 minutes ago" in the active language. */
function relativeTime(iso: string, locale: string, now = Date.now()): string {
  const at = Date.parse(iso);
  if (!Number.isFinite(at)) return "";
  const diff = (at - now) / 1000;
  const abs = Math.abs(diff);
  const rtf = new Intl.RelativeTimeFormat(locale === "ar" ? "ar-EG" : "en", { numeric: "auto" });
  if (abs < 60) return rtf.format(Math.round(diff), "second");
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
  return rtf.format(Math.round(diff / 86400), "day");
}

export function Search() {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const urlQ = (params.get("q") ?? "").trim();
  const uMode = urlMode(params.get("mode"));
  const sid = params.get("sid") ?? "";
  const vp = pageParam(params.get("vp"));
  const tp = pageParam(params.get("tp"));
  const [q, setQ] = useState(urlQ);
  const [mode, setMode] = useState<Mode>(() => uMode ?? readMode());
  const [view, setView] = useStoredView(VIEW_KEY, "list");
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [titles, setTitles] = useState<Loaded<TitleVideo> | null>(null);
  const [transcripts, setTranscripts] = useState<Loaded<TranscriptVideo> | null>(null);
  const [busy, setBusy] = useState<Record<ListKind, boolean>>({
    titles: false,
    transcripts: false,
  });
  const [notice, setNotice] = useState(false);
  const [youtube, setYoutube] = useState<YTBlock | null>(null);
  const [withYouTube, setWithYouTube] = useState(false);
  // Story 26.8: YouTube search runs through yt-dlp on the server; without
  // it the toggle and the "From YouTube" section are not shown at all.
  const [ytAvailable, setYtAvailable] = useState(false);
  const [ytPending, setYtPending] = useState(false);
  const ytAvailableRef = useRef(false);
  // The last search, so an import can run it again.
  const lastSearch = useRef<{ term: string; mode: Mode; yt: boolean } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const userId = user?.id ?? null;
  // the saved and recent lists come back at once too, and are read again
  const [saved, setSavedState] = useState<SavedSearch[]>(
    () => cachedAnswer<SavedSearch[]>(userId, "search:saved") ?? []
  );
  const [recent, setRecentState] = useState<RecentSearch[]>(
    () => cachedAnswer<RecentSearch[]>(userId, "search:recent") ?? []
  );
  const setSaved = useCallback(
    (v: SavedSearch[]) => {
      keepAnswer(userId, "search:saved", v);
      setSavedState(v);
    },
    [userId]
  );
  const setRecent = useCallback(
    (v: RecentSearch[]) => {
      keepAnswer(userId, "search:recent", v);
      setRecentState(v);
    },
    [userId]
  );
  const suggestTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Only the newest search (or open-by-sid) may land; page loads of an older
  // one are dropped too.
  const gen = useRef(0);
  const ytSeq = useRef(0);
  const suggestSeq = useRef(0);
  const snapRef = useRef<Snapshot | null>(null);
  const titlesRef = useRef<Loaded<TitleVideo> | null>(null);
  const transcriptsRef = useRef<Loaded<TranscriptVideo> | null>(null);
  const inflight = useRef<Record<ListKind, string>>({ titles: "", transcripts: "" });
  const alive = useRef(true);
  // The query, mode and sid the URL effect last started.
  const lastRun = useRef<string | null>(null);
  // The pages the URL asks for, for a refresh started by an expired page.
  const wantRef = useRef({ vp, tp });
  wantRef.current = { vp, tp };
  // The latest mode, view and YouTube toggle, for searches started by the URL.
  const modeRef = useRef(mode);
  modeRef.current = mode;
  const viewRef = useRef<View>(view);
  viewRef.current = view;
  const ytRef = useRef(withYouTube);
  ytRef.current = withYouTube;

  useEffect(() => {
    try {
      localStorage.setItem(MODE_KEY, mode);
    } catch {
      /* ignored */
    }
  }, [mode]);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      if (suggestTimer.current) clearTimeout(suggestTimer.current);
    };
  }, []);

  const refreshSaved = () =>
    api
      .get<{ items: SavedSearch[] }>("/api/search/saved")
      .then((r) => setSaved(listOf(r?.items)))
      .catch(() => setSaved([]));

  const refreshRecent = useCallback(
    () =>
      api
        .get<{ items: RecentSearch[] }>("/api/search/recent")
        .then((r) => setRecent(listOf(r?.items).filter((x) => typeof x.search_id === "string")))
        .catch(() => setRecent([])),
    [setRecent]
  );

  useEffect(() => {
    void refreshSaved();
    void refreshRecent();
  }, [refreshRecent]);

  useEffect(() => {
    let cancelled = false;
    api
      .get<{ available: boolean }>("/api/search/youtube/status")
      .then((r) => {
        if (cancelled) return;
        ytAvailableRef.current = r?.available === true;
        setYtAvailable(ytAvailableRef.current);
      })
      .catch(() => {
        if (!cancelled) setYtAvailable(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  /** Shows one saved search's pages (both lists from the same sid). */
  const commit = useCallback(
    (
      s: Snapshot | null,
      vPage: number,
      vItems: TitleVideo[],
      tPage: number,
      tItems: TranscriptVideo[]
    ) => {
      snapRef.current = s;
      setSnap(s);
      const a = s ? { sid: s.sid, page: vPage, items: vItems } : null;
      const b = s ? { sid: s.sid, page: tPage, items: tItems } : null;
      titlesRef.current = a;
      transcriptsRef.current = b;
      setTitles(a);
      setTranscripts(b);
      inflight.current = { titles: "", transcripts: "" };
      setBusy({ titles: false, transcripts: false });
      if (s?.sid) {
        keepAnswer(userId, keptKey(s.sid, vPage, tPage), {
          s,
          vPage,
          vItems,
          tPage,
          tItems,
        } satisfies KeptSearch);
      }
    },
    [userId]
  );

  const clearResults = useCallback(() => {
    gen.current += 1;
    ytSeq.current += 1;
    commit(null, 1, [], 1, []);
    setYoutube(null);
    setYtPending(false);
    setErr(null);
    setPending(false);
    setNotice(false);
  }, [commit]);

  const runYouTube = useCallback(async (query: string) => {
    const seq = ++ytSeq.current;
    setYtPending(true);
    try {
      const block = await api.get<YTBlock>(`/api/search/youtube?q=${encodeURIComponent(query)}`);
      if (seq === ytSeq.current) {
        setYoutube({
          reason: block?.reason,
          items: listOf(block?.items).filter((it) => it.youtube_id),
        });
      }
    } catch {
      if (seq === ytSeq.current) setYoutube({ items: [], reason: "error" });
    } finally {
      if (seq === ytSeq.current) setYtPending(false);
    }
  }, []);

  /** Shows the URL of a saved search; a replace unless `push`. */
  const showUrl = useCallback(
    (s: SearchState, push = false) => {
      if (!alive.current) return;
      setParams(searchParamsOf(s), { replace: !push });
    },
    [setParams]
  );

  /**
   * POSTs the search and shows pages `want` of the new saved search
   * (clamped to its last pages). `notice` says the results were refreshed;
   * `youtube` (when set) also runs or clears the YouTube leg.
   */
  const postSearch = useCallback(
    async (
      query: string,
      m: Mode,
      want: { vp: number; tp: number },
      opts: { notice?: boolean; youtube?: boolean } = {}
    ) => {
      const g = ++gen.current;
      lastSearch.current = {
        term: query,
        mode: m,
        yt: opts.youtube ?? lastSearch.current?.yt ?? false,
      };
      const withYt = opts.youtube === true && ytAvailableRef.current;
      if (opts.youtube !== undefined && !withYt) {
        ytSeq.current += 1;
        setYoutube(null);
        setYtPending(false);
      }
      setPending(true);
      setErr(null);
      try {
        const res = await api.post<SearchResponse>("/api/search", {
          q: query,
          mode: m,
          view: viewRef.current,
        });
        if (g !== gen.current) return;
        let vItems = listOf(res?.videos);
        let tItems = listOf(res?.transcript_videos);
        const s: Snapshot = {
          sid: typeof res?.search_id === "string" ? res.search_id : "",
          q: query,
          mode: m,
          pageSize:
            res?.page_size && res.page_size > 0 ? res.page_size : defaultPageSize(viewRef.current),
          passagesPageSize:
            res?.passages_page_size && res.passages_page_size > 0
              ? res.passages_page_size
              : DEFAULT_PASSAGES_PAGE_SIZE,
          titlesTotal: total(res?.videos_total, vItems.length),
          transcriptsTotal: total(res?.transcript_total, tItems.length),
        };
        // Without a sid (an admin token) there is nothing to page.
        const vPage = s.sid ? Math.min(want.vp, pageCount(s.titlesTotal, s.pageSize)) : 1;
        const tPage = s.sid ? Math.min(want.tp, pageCount(s.transcriptsTotal, s.pageSize)) : 1;
        if (vPage > 1 || tPage > 1) {
          const [a, b] = await Promise.all([
            vPage > 1 ? fetchPage<TitleVideo>("titles", s.sid, vPage, s.pageSize) : null,
            tPage > 1 ? fetchPage<TranscriptVideo>("transcripts", s.sid, tPage, s.pageSize) : null,
          ]);
          if (g !== gen.current) return;
          if (a) vItems = listOf(a.items);
          if (b) tItems = listOf(b.items);
        }
        rememberSizes(s.sid, s);
        commit(s, vPage, vItems, tPage, tItems);
        setNotice(Boolean(opts.notice));
        if (s.sid) {
          lastRun.current = runKey(query, m, s.sid);
          showUrl({ q: query, mode: m, sid: s.sid, vp: vPage, tp: tPage });
        }
        void refreshRecent();
      } catch (e) {
        if (g !== gen.current) return;
        setErr(errorText(e, t));
        commit(null, 1, [], 1, []);
      } finally {
        if (g === gen.current) setPending(false);
      }
      // Story 26.8: the YouTube leg runs after the local results are on
      // screen. A yt-dlp search takes seconds and must never hold them up.
      if (withYt && g === gen.current) void runYouTube(query);
    },
    [commit, refreshRecent, runYouTube, showUrl, t]
  );

  /** Opens a saved search by its sid (a reload, Back, a recent search). */
  const openBySid = useCallback(
    async (
      query: string,
      m: Mode,
      s: string,
      want: { vp: number; tp: number },
      yt: boolean,
      quiet = false
    ) => {
      const g = ++gen.current;
      lastSearch.current = { term: query, mode: m, yt };
      if (!quiet) setPending(true);
      setErr(null);
      const sizes = recallSizes(s) ?? {
        pageSize: defaultPageSize(viewRef.current),
        passagesPageSize: DEFAULT_PASSAGES_PAGE_SIZE,
      };
      try {
        let [a, b] = await Promise.all([
          fetchPage<TitleVideo>("titles", s, want.vp, sizes.pageSize),
          fetchPage<TranscriptVideo>("transcripts", s, want.tp, sizes.pageSize),
        ]);
        if (g !== gen.current) return;
        const size = a?.limit && a.limit > 0 ? a.limit : sizes.pageSize;
        const snapshot: Snapshot = {
          sid: s,
          q: query,
          mode: m,
          pageSize: size,
          passagesPageSize: sizes.passagesPageSize,
          titlesTotal: total(a?.total, 0),
          transcriptsTotal: total(b?.total, 0),
        };
        // A page past the end (a hand-made link) shows the last page.
        const vPage = Math.min(want.vp, pageCount(snapshot.titlesTotal, size));
        const tPage = Math.min(want.tp, pageCount(snapshot.transcriptsTotal, size));
        if (vPage !== want.vp || tPage !== want.tp) {
          const [a2, b2] = await Promise.all([
            vPage !== want.vp ? fetchPage<TitleVideo>("titles", s, vPage, size) : null,
            tPage !== want.tp ? fetchPage<TranscriptVideo>("transcripts", s, tPage, size) : null,
          ]);
          if (g !== gen.current) return;
          if (a2) a = a2;
          if (b2) b = b2;
          showUrl({ q: query, mode: m, sid: s, vp: vPage, tp: tPage });
        }
        commit(snapshot, vPage, listOf(a?.items), tPage, listOf(b?.items));
        setNotice(false);
        void refreshRecent();
      } catch (e) {
        if (g !== gen.current) return;
        if (isSearchExpired(e)) {
          void postSearch(query, m, want, { notice: true, youtube: yt });
          return;
        }
        setErr(errorText(e, t));
        commit(null, 1, [], 1, []);
      } finally {
        if (g === gen.current) setPending(false);
      }
      if (g !== gen.current) return;
      if (yt && ytAvailableRef.current) void runYouTube(query);
      else {
        ytSeq.current += 1;
        setYoutube(null);
        setYtPending(false);
      }
    },
    [commit, postSearch, refreshRecent, runYouTube, showUrl, t]
  );

  /** Loads one page of one list from the saved search on screen. */
  const loadPage = useCallback(
    async (list: ListKind, page: number) => {
      const s = snapRef.current;
      if (!s?.sid) return;
      const key = `${s.sid}:${page}`;
      if (inflight.current[list] === key) return;
      inflight.current[list] = key;
      const g = gen.current;
      setBusy((b) => ({ ...b, [list]: true }));
      try {
        const r = await fetchPage<TitleVideo & TranscriptVideo>(list, s.sid, page, s.pageSize);
        if (g !== gen.current || inflight.current[list] !== key) return;
        const loaded = { sid: s.sid, page, items: listOf(r?.items) };
        if (list === "titles") {
          titlesRef.current = loaded;
          setTitles(loaded);
        } else {
          transcriptsRef.current = loaded;
          setTranscripts(loaded);
        }
      } catch (e) {
        if (g !== gen.current || inflight.current[list] !== key) return;
        if (isSearchExpired(e)) {
          void postSearch(s.q, s.mode, wantRef.current, { notice: true });
          return;
        }
        toast.show({
          tone: "error",
          message: errorText(e, t),
        });
      } finally {
        if (inflight.current[list] === key) {
          inflight.current[list] = "";
          setBusy((b) => ({ ...b, [list]: false }));
        }
      }
    },
    [postSearch, t, toast]
  );

  /** A passage list found its saved search expired: refresh, keep the pages. */
  const onExpired = useCallback(() => {
    const s = snapRef.current;
    if (s) void postSearch(s.q, s.mode, wantRef.current, { notice: true });
  }, [postSearch]);

  // The URL drives the search: a deep link, the header search box, Back /
  // Forward, a recent search and the form all land here. With the sid on
  // screen only the pages change; `lastRun` keeps a re-run of the same
  // effect (StrictMode) from sending a second request.
  useEffect(() => {
    if (!urlQ) {
      lastRun.current = "";
      setQ("");
      clearResults();
      return;
    }
    const m = uMode ?? modeRef.current;
    const s = snapRef.current;
    if (sid && s && s.sid === sid) {
      if (titlesRef.current?.page !== vp) void loadPage("titles", vp);
      if (transcriptsRef.current?.page !== tp) void loadPage("transcripts", tp);
      return;
    }
    const key = runKey(urlQ, m, sid);
    if (lastRun.current === key) return;
    lastRun.current = key;
    setQ(urlQ);
    if (uMode) setMode(uMode);
    if (sid) {
      // a return to a search already seen: its pages at once, read again quietly
      const kept = cachedAnswer<KeptSearch>(userId, keptKey(sid, vp, tp));
      if (kept && kept.s.q === urlQ && kept.s.mode === m) {
        commit(kept.s, kept.vPage, kept.vItems, kept.tPage, kept.tItems);
      }
      void openBySid(urlQ, m, sid, { vp, tp }, ytRef.current, Boolean(kept));
    } else void postSearch(urlQ, m, { vp: 1, tp: 1 }, { youtube: ytRef.current });
  }, [urlQ, uMode, sid, vp, tp, clearResults, loadPage, openBySid, postSearch, commit, userId]);

  // Re-run the last search (after a YouTube import, so the result shows
  // its new state).
  const rerun = () => {
    const last = lastSearch.current;
    if (last) void postSearch(last.term, last.mode, wantRef.current, { youtube: last.yt });
  };

  /** Shows `term` in the URL (a history entry) and searches it. */
  function search(term: string, m: Mode) {
    const query = term.trim();
    setSuggestions([]);
    if (!query) {
      if (urlQ) {
        lastRun.current = "";
        setParams(new URLSearchParams());
      }
      clearResults();
      return;
    }
    // Same query and mode again (a retry): the URL entry is replaced.
    const same = query === urlQ && m === (uMode ?? modeRef.current);
    lastRun.current = runKey(query, m, "");
    setParams(searchParamsOf({ q: query, mode: m }), { replace: same });
    void postSearch(query, m, { vp: 1, tp: 1 }, { youtube: withYouTube });
  }

  function onChangeQuery(value: string) {
    setQ(value);
    if (suggestTimer.current) clearTimeout(suggestTimer.current);
    const seq = ++suggestSeq.current;
    if (!value.trim()) {
      setSuggestions([]);
      return;
    }
    suggestTimer.current = setTimeout(() => {
      api
        .get<{ suggestions: string[] }>(`/api/search/suggest?q=${encodeURIComponent(value.trim())}`)
        .then((r) => {
          if (seq !== suggestSeq.current) return;
          setSuggestions(
            Array.isArray(r?.suggestions)
              ? r.suggestions.filter((x): x is string => typeof x === "string")
              : []
          );
        })
        .catch(() => {
          if (seq === suggestSeq.current) setSuggestions([]);
        });
    }, 200);
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    suggestSeq.current += 1;
    if (suggestTimer.current) clearTimeout(suggestTimer.current);
    search(q, mode);
  }

  function saveCurrent() {
    if (!q.trim()) return;
    void api
      .post("/api/search/save", { q: q.trim(), mode })
      .then(refreshSaved)
      .catch((e) =>
        toast.show({
          message: errorText(e, t),
          tone: "error",
        })
      );
  }

  function goPage(list: ListKind, page: number) {
    const s = snapRef.current;
    if (!s?.sid) return;
    showUrl(
      {
        q: s.q,
        mode: s.mode,
        sid: s.sid,
        vp: list === "titles" ? page : vp,
        tp: list === "transcripts" ? page : tp,
      },
      true
    );
  }

  function backToFirst() {
    const s = snapRef.current;
    setNotice(false);
    if (s?.sid) showUrl({ q: s.q, mode: s.mode, sid: s.sid }, true);
  }

  function openRecent(r: RecentSearch) {
    const m = asMode(r.mode);
    setQ(r.q);
    setMode(m);
    if (!r.live) {
      search(r.q, m);
      return;
    }
    setNotice(false);
    showUrl({ q: r.q, mode: m, sid: r.search_id }, true);
  }

  const removeRecent = (id: string) =>
    void api
      .delete(`/api/search/recent/${encodeURIComponent(id)}`)
      .then(refreshRecent)
      .catch(() => {});
  const clearRecent = () =>
    void api
      .delete("/api/search/recent")
      .then(refreshRecent)
      .catch(() => {});

  // Links from the results carry the search and both pages, so the
  // matches page can lead back to exactly this screen.
  const here: SearchState | null = snap
    ? {
        q: snap.q,
        mode: snap.mode,
        sid: snap.sid,
        vp: titles?.page ?? 1,
        tp: transcripts?.page ?? 1,
      }
    : null;
  const nothing = snap && !err && snap.titlesTotal === 0 && snap.transcriptsTotal === 0;

  return (
    <section className="mkt-page mkt-search">
      <div className="mkt-search__layout">
        <div className="mkt-search__main">
          <form onSubmit={onSubmit} className="mkt-search__form" role="search">
            <input
              type="search"
              value={q}
              onChange={(e) => onChangeQuery(e.target.value)}
              placeholder={t("search.placeholder")}
              aria-label={t("nav.search")}
              list="mkt-suggest"
              autoFocus
            />
            <datalist id="mkt-suggest">
              {suggestions.map((s) => (
                <option key={s} value={s} />
              ))}
            </datalist>
            <select
              value={mode}
              onChange={(e) => setMode(e.target.value as Mode)}
              aria-label={t("search.mode")}
            >
              <option value="hybrid">{t("search.mode.hybrid")}</option>
              <option value="fts">{t("search.mode.fts")}</option>
              <option value="semantic">{t("search.mode.semantic")}</option>
            </select>
            <Button type="submit" loading={pending}>
              {t("nav.search")}
            </Button>
            <Button type="button" variant="secondary" onClick={saveCurrent}>
              {t("search.save")}
            </Button>
            <PageHelp />
            {ytAvailable && (
              <label className="mkt-search__yt-toggle">
                <input
                  type="checkbox"
                  checked={withYouTube}
                  onChange={(e) => setWithYouTube(e.target.checked)}
                />
                {t("search.youtube.toggle")}
              </label>
            )}
          </form>

          {notice && (
            <div className="mkt-search__notice" role="status">
              <span>{t("search.refreshed")}</span>
              <button type="button" className="mkt-link-btn" onClick={backToFirst}>
                {t("search.backToFirst")}
              </button>
              <button
                type="button"
                className="mkt-icon-btn mkt-search__notice-close"
                aria-label={t("common.dismiss")}
                onClick={() => setNotice(false)}
              >
                ×
              </button>
            </div>
          )}

          {err && <ErrorState kind="server" title={t("common.error")} description={err} />}

          {nothing && <EmptyState kind="filtered_out" title={t("search.didYouMean")} />}

          {snap && here && !nothing && (
            <div className="mkt-search__toolbar">
              <ViewToggle view={view} onChange={setView} />
            </div>
          )}

          {snap && here && titles && snap.titlesTotal > 0 && (
            <ResultSection
              id="titles"
              heading={t("search.videos")}
              page={titles.page}
              total={snap.titlesTotal}
              pageSize={snap.pageSize}
              canPage={Boolean(snap.sid)}
              busy={busy.titles}
              onPage={(p) => goPage("titles", p)}
            >
              <ul
                className={`mkt-sresults mkt-sresults--${view}`}
                data-testid="search-videos"
                aria-label={t("search.videos")}
              >
                {titles.items.map((v) => (
                  <VideoResult
                    key={v.video_id}
                    video={v}
                    view={view}
                    href={matchesHref(v.video_id, here)}
                  />
                ))}
              </ul>
            </ResultSection>
          )}

          {snap && here && transcripts && snap.transcriptsTotal > 0 && (
            <ResultSection
              id="transcripts"
              heading={t("search.passages")}
              page={transcripts.page}
              total={snap.transcriptsTotal}
              pageSize={snap.pageSize}
              canPage={Boolean(snap.sid)}
              busy={busy.transcripts}
              onPage={(p) => goPage("transcripts", p)}
            >
              <ul
                className={`mkt-sresults mkt-sresults--${view}`}
                data-testid="search-transcripts"
                aria-label={t("search.passages")}
              >
                {transcripts.items.map((v) => (
                  <TranscriptResult
                    key={`${transcripts.sid}:${v.video_id}`}
                    video={v}
                    view={view}
                    href={matchesHref(v.video_id, here)}
                    sid={snap.sid}
                    passagesPageSize={snap.passagesPageSize}
                    onExpired={onExpired}
                  />
                ))}
              </ul>
            </ResultSection>
          )}

          {ytAvailable && withYouTube && (ytPending || youtube) && (
            <YouTubeSection
              block={youtube}
              pending={ytPending}
              canImport={Boolean(user?.is_admin)}
              localVideos={titles?.items ?? []}
              onImported={rerun}
            />
          )}
        </div>

        <div className="mkt-search__side">
          <aside aria-label={t("search.saved")}>
            <h2>{t("search.saved")}</h2>
            {saved.length === 0 ? (
              <p className="mkt-muted">{t("common.empty")}</p>
            ) : (
              <ul className="mkt-list">
                {saved.map((s) => (
                  <li key={s.id}>
                    <button
                      type="button"
                      className="mkt-link-btn"
                      onClick={() => {
                        const m = asMode(s.mode);
                        setQ(s.q);
                        setMode(m);
                        search(s.q, m);
                      }}
                    >
                      {s.q}
                    </button>
                    <button
                      type="button"
                      className="mkt-icon-btn"
                      aria-label={`${t("common.delete")} ${s.q}`}
                      onClick={() =>
                        void api
                          .delete(`/api/search/saved/${encodeURIComponent(s.id)}`)
                          .then(refreshSaved)
                          .catch(() => {})
                      }
                    >
                      ×
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </aside>
          <RecentSearches
            items={recent}
            onOpen={openRecent}
            onRemove={removeRecent}
            onClear={clearRecent}
            n={n}
          />
        </div>
      </div>
    </section>
  );
}

/** A list's heading, its rows and its pager ("Page 2 of 5 · 93 videos"). */
function ResultSection({
  id,
  heading,
  page,
  total: count,
  pageSize,
  canPage,
  busy,
  onPage,
  children,
}: {
  id: ListKind;
  heading: string;
  page: number;
  total: number;
  pageSize: number;
  canPage: boolean;
  busy: boolean;
  onPage: (page: number) => void;
  children: ReactNode;
}) {
  const { t, n } = useI18n();
  const pages = pageCount(count, pageSize);
  return (
    <section
      className="mkt-sresults-section"
      aria-labelledby={`mkt-sr-${id}`}
      aria-busy={busy}
      data-testid={`section-${id}`}
    >
      <h2 id={`mkt-sr-${id}`} className="mkt-results__heading">
        {heading}
      </h2>
      {children}
      <div className="mkt-search__pager">
        <span className="mkt-muted" data-testid={`pageinfo-${id}`}>
          {t("search.pageOf", { page: n(page), pages: n(pages), total: n(count) })}
        </span>
        {canPage && (
          <Pagination
            page={page}
            pageCount={pages}
            onChange={onPage}
            label={heading}
            previousLabel={t("pagination.previous")}
            nextLabel={t("pagination.next")}
            pageLabel={(p) => t("pagination.page", { page: n(p) })}
          />
        )}
      </div>
    </section>
  );
}

function VideoTitle({ video }: { video: TitleVideo }) {
  if (video.title_snippet) return <>{renderSnippet(video.title_snippet)}</>;
  return <span dir="auto">{video.title}</span>;
}

/**
 * One video in either list. List view: a row with poster, title, library
 * and duration; grid view: a card. The poster and title open the video's
 * matches page; `children` adds the list-specific parts.
 */
function VideoResult({
  video,
  view,
  href,
  badges,
  children,
}: {
  video: TitleVideo;
  view: View;
  href: string;
  badges?: ReactNode;
  children?: ReactNode;
}) {
  const duration =
    typeof video.duration_sec === "number" ? formatDuration(video.duration_sec) : null;
  if (view === "grid") {
    return (
      <li className="mkt-card mkt-scard">
        <Link to={href} className="mkt-scard__link">
          <VideoPoster videoId={video.video_id} src={video.poster_url} />
          <span className="mkt-card__title mkt-result__title">
            <VideoTitle video={video} />
          </span>
          {duration && <span className="mkt-card__meta">{duration}</span>}
        </Link>
        {badges && <div className="mkt-svideo__badges">{badges}</div>}
        {children}
      </li>
    );
  }
  return (
    <li className="mkt-svideo">
      <Link to={href} className="mkt-svideo__poster" tabIndex={-1} aria-hidden="true">
        <VideoPoster videoId={video.video_id} src={video.poster_url} />
      </Link>
      <div className="mkt-svideo__body">
        <Link to={href} className="mkt-svideo__title mkt-result__title">
          <VideoTitle video={video} />
        </Link>
        <div className="mkt-svideo__meta mkt-muted">
          {video.library_name && (
            <span className="mkt-svideo__library" dir="auto">
              {video.library_name}
            </span>
          )}
          {duration && <span className="mkt-svideo__duration">{duration}</span>}
        </div>
        {badges && <div className="mkt-svideo__badges">{badges}</div>}
        {children}
      </div>
    </li>
  );
}

function TranscriptResult({
  video,
  view,
  href,
  sid,
  passagesPageSize,
  onExpired,
}: {
  video: TranscriptVideo;
  view: View;
  href: string;
  sid: string;
  passagesPageSize: number;
  onExpired: () => void;
}) {
  const { t, n } = useI18n();
  // Grid cards keep their passages folded until asked.
  const [open, setOpen] = useState(false);
  const badges = (
    <>
      <Badge tone="accent">{matchesLabel(t, n, video.matches)}</Badge>
      {video.in_title && <Badge tone="success">{t("search.alsoInTitle")}</Badge>}
    </>
  );
  const passages = (
    <PassageList video={video} sid={sid} pageSize={passagesPageSize} onExpired={onExpired} />
  );
  return (
    <VideoResult video={video} view={view} href={href} badges={badges}>
      {view === "grid" ? (
        <>
          <Button size="sm" variant="ghost" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            {open ? t("search.hidePassages") : t("search.showPassages")}
          </Button>
          {open && passages}
        </>
      ) : (
        passages
      )}
    </VideoResult>
  );
}

/**
 * A video's passages: the top few from the results, then (Show all) every
 * matching passage, best first, a page at a time. Each opens the player at
 * its time.
 */
function PassageList({
  video,
  sid,
  pageSize,
  onExpired,
}: {
  video: TranscriptVideo;
  sid: string;
  pageSize: number;
  onExpired: () => void;
}) {
  const { t, n, dir } = useI18n();
  const toast = useToast();
  const [all, setAll] = useState<{ items: Passage[]; total: number } | null>(null);
  const [loading, setLoading] = useState(false);
  const top = listOf(video.passages);
  const shown = all ? all.items : top;

  async function load(offset: number) {
    setLoading(true);
    try {
      const r = await api.get<PassagesPage>(
        `/api/search/passages?sid=${encodeURIComponent(sid)}&video_id=${encodeURIComponent(
          video.video_id
        )}&order=score&offset=${offset}&limit=${pageSize}`
      );
      const items = listOf(r?.items);
      setAll((prev) => {
        const base = offset === 0 || !prev ? [] : prev.items;
        const seen = new Set(base.map((p) => p.segment_id));
        return {
          items: [...base, ...items.filter((p) => !seen.has(p.segment_id))],
          total: typeof r?.total === "number" ? r.total : video.matches,
        };
      });
    } catch (e) {
      if (isSearchExpired(e)) {
        onExpired();
        return;
      }
      toast.show({
        tone: "error",
        message: errorText(e, t),
      });
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="mkt-passages">
      {shown.length > 0 && (
        <ul className="mkt-results mkt-passages__list">
          {shown.map((p) => (
            <li key={p.segment_id} className="mkt-result">
              <Link to={watchHref(video.video_id, p.start_sec)}>
                <span className="mkt-result__time">
                  [{ts(p.start_sec)} {dir === "rtl" ? "←" : "→"} {ts(p.end_sec)}]
                </span>
                <span className="mkt-result__snippet">{renderSnippet(p.snippet)}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      <div className="mkt-passages__more">
        {!all && sid && video.matches > top.length && (
          <Button size="sm" variant="ghost" loading={loading} onClick={() => void load(0)}>
            {t("search.showAll", { count: n(video.matches) })}
          </Button>
        )}
        {all && all.items.length < all.total && (
          <Button
            size="sm"
            variant="secondary"
            loading={loading}
            onClick={() => void load(all.items.length)}
          >
            {t("common.loadMore")}
          </Button>
        )}
        {all && (
          <Button size="sm" variant="ghost" onClick={() => setAll(null)}>
            {t("search.showFewer")}
          </Button>
        )}
      </div>
    </div>
  );
}

/** The user's recent searches: open a live one by its sid, re-run an expired one. */
function RecentSearches({
  items,
  onOpen,
  onRemove,
  onClear,
  n,
}: {
  items: RecentSearch[];
  onOpen: (r: RecentSearch) => void;
  onRemove: (sid: string) => void;
  onClear: () => void;
  n: (v: number) => string;
}) {
  const { t, locale, formatDate } = useI18n();
  return (
    <aside aria-label={t("search.recent")} className="mkt-recent">
      <div className="mkt-recent__head">
        <h2>{t("search.recent")}</h2>
        {items.length > 0 && (
          <button type="button" className="mkt-link-btn" onClick={onClear}>
            {t("search.recent.clear")}
          </button>
        )}
      </div>
      {items.length === 0 ? (
        <p className="mkt-muted">{t("common.empty")}</p>
      ) : (
        <ul className="mkt-list">
          {items.map((r) => (
            <li key={r.search_id} className="mkt-recent__item">
              <button type="button" className="mkt-recent__open" onClick={() => onOpen(r)}>
                <span className="mkt-recent__q" dir="auto">
                  {r.q}
                </span>
                <span className="mkt-recent__meta mkt-muted">
                  {t(`search.mode.${asMode(r.mode)}`)} ·{" "}
                  <time dateTime={r.created_at} title={formatDate(r.created_at)}>
                    {relativeTime(r.created_at, locale)}
                  </time>
                </span>
                <span className="mkt-recent__meta mkt-muted">
                  {t("search.recent.counts", {
                    titles: n(r.title_count ?? 0),
                    transcripts: n(r.transcript_count ?? 0),
                  })}
                </span>
                <Badge tone={r.live ? "success" : "neutral"}>
                  {r.live ? t("search.recent.live") : t("search.recent.expired")}
                </Badge>
              </button>
              <button
                type="button"
                className="mkt-icon-btn"
                aria-label={t("search.recent.remove", { q: r.q })}
                onClick={() => onRemove(r.search_id)}
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
    </aside>
  );
}

// YouTubeSection renders the "From YouTube" augmentation (Story 26.8),
// visually distinct from local results. Each card links out to YouTube
// (rel="noopener noreferrer"); a match badge links an already-in-library
// result to the local video, and every other result has an Import button
// that stages its metadata onto a local video the user picks. A
// non-empty `reason` renders a quiet inline note, never an error toast.
function YouTubeSection({
  block,
  pending,
  canImport,
  localVideos,
  onImported,
}: {
  block: YTBlock | null;
  pending: boolean;
  /** Importing is an editor (admin) write; the server refuses others. */
  canImport: boolean;
  localVideos: VideoCard[];
  onImported: () => void;
}) {
  const { t, n } = useI18n();
  const [importing, setImporting] = useState<YTItem | null>(null);

  // "disabled" means yt-dlp went away since the page loaded: show nothing.
  if (!pending && block?.reason === "disabled") return null;
  const items = block?.items ?? [];

  return (
    <section
      className="mkt-yt-section"
      aria-label={t("search.youtube.heading")}
      aria-busy={pending}
    >
      <h2 className="mkt-yt-section__title">{t("search.youtube.heading")}</h2>
      {pending && items.length === 0 ? (
        <p className="mkt-muted" role="status">
          {t("search.youtube.loading")}
        </p>
      ) : items.length === 0 ? (
        <p className="mkt-muted">
          {block?.reason && YT_REASONS.has(block.reason)
            ? t(`search.youtube.reason.${block.reason}`)
            : t("search.youtube.none")}
        </p>
      ) : (
        <ul className="mkt-yt-list" role="list">
          {items.map((it) => (
            <li key={it.youtube_id} className="mkt-yt-card">
              <a
                href={`https://www.youtube.com/watch?v=${encodeURIComponent(it.youtube_id)}`}
                target="_blank"
                rel="noopener noreferrer"
                className="mkt-yt-card__link"
              >
                {it.thumbnail && <img src={it.thumbnail} alt="" loading="lazy" />}
                <span className="mkt-yt-card__title" dir="auto">
                  {it.title}
                </span>
                <span className="mkt-yt-card__channel mkt-muted" dir="auto">
                  {[
                    it.channel,
                    typeof it.duration_sec === "number" ? ts(it.duration_sec) : "",
                    it.published_at ?? "",
                    typeof it.view_count === "number"
                      ? t("search.youtube.views", { count: n(it.view_count) })
                      : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </a>
              <div className="mkt-yt-card__match">
                {it.match?.state === "in_library" && it.match.video_id ? (
                  <Link to={`/videos/${it.match.video_id}`}>
                    <Badge tone="success">{t("search.youtube.inLibrary")}</Badge>
                  </Link>
                ) : (
                  <>
                    {it.match?.state === "imported" && it.match.video_id && (
                      <Link to={`/videos/${it.match.video_id}`}>
                        <Badge tone="accent">{t("search.youtube.importedBadge")}</Badge>
                      </Link>
                    )}
                    {canImport && (
                      <Button size="sm" variant="secondary" onClick={() => setImporting(it)}>
                        {t("search.youtube.import")}
                      </Button>
                    )}
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {importing && (
        <ImportModal
          item={importing}
          localVideos={localVideos}
          onClose={() => setImporting(null)}
          onImported={() => {
            setImporting(null);
            onImported();
          }}
        />
      )}
    </section>
  );
}

// ImportModal picks the local video a YouTube result's metadata is staged
// onto. It starts with the title matches of the current search and can
// look up any other video by title. Staging writes an enrichment
// candidate: the fields reach the video when it is accepted on the
// video's Metadata tab, so nothing the user set by hand is overwritten.
function ImportModal({
  item,
  localVideos,
  onClose,
  onImported,
}: {
  item: YTItem;
  localVideos: VideoCard[];
  onClose: () => void;
  onImported: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [find, setFind] = useState("");
  const [found, setFound] = useState<VideoCard[] | null>(null);
  const [finding, setFinding] = useState(false);
  const [chosen, setChosen] = useState<VideoCard | null>(null);
  const [busy, setBusy] = useState(false);

  const choices = found ?? localVideos;

  async function lookUp(e: FormEvent) {
    e.preventDefault();
    const q = find.trim();
    if (!q) {
      setFound(null);
      return;
    }
    setFinding(true);
    try {
      const res = await api.post<SearchResponse>("/api/search", {
        q,
        mode: "fts",
        view: "list",
        save: false,
      });
      setFound(listOf(res?.videos));
    } catch (err) {
      setFound([]);
      toast.show({
        tone: "error",
        message: errorText(err, t),
      });
    } finally {
      setFinding(false);
    }
  }

  async function confirm() {
    if (!chosen) return;
    setBusy(true);
    try {
      await api.post(`/api/videos/${encodeURIComponent(chosen.video_id)}/import-youtube`, {
        youtube_id: item.youtube_id,
        title: item.title,
        channel: item.channel,
        description: item.description_snippet,
        thumbnail: item.thumbnail,
        ...(item.published_at ? { published_at: item.published_at } : {}),
        ...(typeof item.duration_sec === "number" ? { duration_sec: item.duration_sec } : {}),
      });
      toast.show({
        tone: "success",
        message: t("search.youtube.imported", { title: chosen.title }),
      });
      onImported();
    } catch (err) {
      setBusy(false);
      toast.show({
        tone: "error",
        message: errorText(err, t),
      });
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={t("search.youtube.importTitle")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={confirm} loading={busy} disabled={!chosen}>
            {t("search.youtube.importConfirm")}
          </Button>
        </>
      }
    >
      <p className="mkt-muted" dir="auto">
        {t("search.youtube.importIntro", { title: item.title })}
      </p>
      <form className="mkt-search__form" onSubmit={lookUp} role="search">
        <input
          type="search"
          value={find}
          onChange={(e) => setFind(e.target.value)}
          placeholder={t("search.youtube.importFind")}
          aria-label={t("search.youtube.importFind")}
        />
        <Button type="submit" variant="secondary" loading={finding}>
          {t("nav.search")}
        </Button>
      </form>
      {choices.length === 0 ? (
        <p className="mkt-muted">{t("search.youtube.importNoVideos")}</p>
      ) : (
        <ul className="mkt-yt-import__list" aria-label={t("search.youtube.importChoose")}>
          {choices.map((v) => (
            <li key={v.video_id}>
              <label className="mkt-yt-import__choice">
                <input
                  type="radio"
                  name="yt-import-target"
                  checked={chosen?.video_id === v.video_id}
                  onChange={() => setChosen(v)}
                />
                <span dir="auto">{v.title}</span>
                {v.duration_sec != null && (
                  <span className="mkt-muted"> · {ts(v.duration_sec)}</span>
                )}
              </label>
            </li>
          ))}
        </ul>
      )}
    </Modal>
  );
}
