// Story 11.4 — Search interface.
//
// Real server contract (api/internal/handlers/search):
//   POST   /api/search          { q, mode, limit } -> { hits, total, ... }
//   GET    /api/search/suggest?q=  -> { suggestions: string[] }
//   POST   /api/search/save     { q, mode }
//   GET    /api/search/saved    -> { items:[{ id, q, mode }] }
//   DELETE /api/search/saved/{id}
//
// `?q=` seeds the query from the header search box. Mode (hybrid/fts/
// semantic) persists in localStorage. Suggestions debounce at 200ms.
// Snippet `<mark>` runs are rendered as escaped React nodes (no raw
// HTML). Deep-links use a formatted [mm:ss → mm:ss] range. The hit list
// is window-capped (true virtualization deferred — see report).
import { type FormEvent, type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { useAuth } from "../lib/auth";

type Mode = "hybrid" | "fts" | "semantic";
const SEARCH_LIMIT = 50;
const MODE_KEY = "mkt:search:mode";
const RESULT_WINDOW = 100;

interface ServerHit {
  segment_id: number;
  video_id: string;
  start_sec: number;
  end_sec: number;
  snippet: string;
  score: number;
}
interface YTItem {
  youtube_id: string;
  title: string;
  channel: string;
  description_snippet: string;
  thumbnail: string;
  published_at?: string;
  view_count?: number;
  match?: { state: string; video_id?: string } | null;
}
interface YTBlock {
  items: YTItem[];
  reason?: string;
}
// A whole-video match on its title (a library has titles long before it has
// transcripts).
interface VideoHit {
  video_id: string;
  title: string;
  duration_sec?: number;
}
interface SearchResponse {
  hits: ServerHit[];
  videos?: VideoHit[];
  total: number;
  youtube?: YTBlock;
}
interface SavedSearch {
  id: string;
  q: string;
  mode: string;
}

const MODES: readonly string[] = ["hybrid", "fts", "semantic"];
const asMode = (m: unknown): Mode => (MODES.includes(m as string) ? (m as Mode) : "hybrid");
const YT_REASONS = new Set(["disabled", "no_key", "rate_limited", "error"]);

function readMode(): Mode {
  try {
    const m = localStorage.getItem(MODE_KEY);
    if (m === "hybrid" || m === "fts" || m === "semantic") return m;
  } catch {
    /* ignored */
  }
  return "hybrid";
}

function ts(s: number): string {
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return `${m}:${sec.toString().padStart(2, "0")}`;
}

// Server emits only <mark>…</mark> wrappers; split on the literal tags
// and keep every other run as a plain (escaped) text node.
function renderSnippet(snippet: string | null | undefined): ReactNode[] {
  if (typeof snippet !== "string") return [];
  const parts = snippet.split(/(<mark>|<\/mark>)/);
  const nodes: ReactNode[] = [];
  let inMark = false;
  let key = 0;
  for (const part of parts) {
    if (part === "<mark>") {
      inMark = true;
      continue;
    }
    if (part === "</mark>") {
      inMark = false;
      continue;
    }
    if (part === "") continue;
    nodes.push(
      inMark ? (
        <mark key={key++}>{part}</mark>
      ) : (
        <span key={key++} dir="auto">
          {part}
        </span>
      )
    );
  }
  return nodes;
}

export function Search() {
  const { t, dir } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const urlQ = (params.get("q") ?? "").trim();
  const [q, setQ] = useState(urlQ);
  const [mode, setMode] = useState<Mode>(readMode);
  const [hits, setHits] = useState<ServerHit[] | null>(null);
  const [videos, setVideos] = useState<VideoHit[]>([]);
  const [youtube, setYoutube] = useState<YTBlock | null>(null);
  const [withYouTube, setWithYouTube] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [saved, setSaved] = useState<SavedSearch[]>([]);
  const suggestTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Only the newest search / suggest answer may land.
  const searchSeq = useRef(0);
  const suggestSeq = useRef(0);
  // The latest mode and YouTube toggle, for searches started by the URL.
  const modeRef = useRef(mode);
  modeRef.current = mode;
  const ytRef = useRef(withYouTube);
  ytRef.current = withYouTube;

  useEffect(() => {
    try {
      localStorage.setItem(MODE_KEY, mode);
    } catch {
      /* ignored */
    }
  }, [mode]);

  useEffect(
    () => () => {
      if (suggestTimer.current) clearTimeout(suggestTimer.current);
    },
    []
  );

  const refreshSaved = () =>
    api
      .get<{ items: SavedSearch[] }>("/api/search/saved")
      .then((r) => setSaved(Array.isArray(r?.items) ? r.items.filter((x) => x != null) : []))
      .catch(() => setSaved([]));

  useEffect(() => {
    void refreshSaved();
  }, []);

  const clearResults = useCallback(() => {
    searchSeq.current += 1;
    setHits(null);
    setVideos([]);
    setYoutube(null);
    setErr(null);
    setPending(false);
  }, []);

  const runSearch = useCallback(
    async (term: string, m: Mode, includeYouTube: boolean) => {
      const query = term.trim();
      if (!query) return;
      const seq = ++searchSeq.current;
      setPending(true);
      setErr(null);
      try {
        const res = await api.post<SearchResponse>("/api/search", {
          q: query,
          mode: m,
          limit: SEARCH_LIMIT,
          // Story 26.8: additive opt-in; absent ⇒ local-only behaviour.
          ...(includeYouTube ? { include: ["youtube"] } : {}),
        });
        if (seq !== searchSeq.current) return;
        const list = <T,>(v: T[] | null | undefined) =>
          Array.isArray(v) ? v.filter((x): x is T => x != null) : [];
        setHits(list(res?.hits));
        setVideos(list(res?.videos));
        const yt = res?.youtube;
        setYoutube(
          includeYouTube
            ? { ...(yt ?? {}), items: list(yt?.items).filter((it) => it.youtube_id) }
            : null
        );
      } catch (e) {
        if (seq !== searchSeq.current) return;
        setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
        setHits([]);
        setVideos([]);
        setYoutube(null);
      } finally {
        if (seq === searchSeq.current) setPending(false);
      }
    },
    [t]
  );

  // `?q=` drives the search: a deep link, the header search box, Back /
  // Forward and the form all land here. `lastRun` keeps a re-run of the
  // same effect (StrictMode) from sending a second request.
  const lastRun = useRef<string | null>(null);
  useEffect(() => {
    if (lastRun.current === urlQ) return;
    lastRun.current = urlQ;
    setQ(urlQ);
    if (urlQ) void runSearch(urlQ, modeRef.current, ytRef.current);
    else clearResults();
  }, [urlQ, runSearch, clearResults]);

  /** Shows `term` in the URL (a history entry) and searches it. */
  function search(term: string, m: Mode) {
    const query = term.trim();
    setSuggestions([]);
    if (query === urlQ) {
      // Same query again (a new mode, a retry): the URL does not change.
      if (query) void runSearch(query, m, withYouTube);
      return;
    }
    const nextParams = new URLSearchParams(params);
    if (query) nextParams.set("q", query);
    else nextParams.delete("q");
    lastRun.current = query;
    setParams(nextParams);
    if (query) void runSearch(query, m, withYouTube);
    else clearResults();
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
          message: e instanceof ApiError ? e.problem.title : t("common.error"),
          tone: "error",
        })
      );
  }

  const shown = hits ? hits.slice(0, RESULT_WINDOW) : null;

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
            <label className="mkt-search__yt-toggle">
              <input
                type="checkbox"
                checked={withYouTube}
                onChange={(e) => setWithYouTube(e.target.checked)}
              />
              {t("search.youtube.toggle")}
            </label>
          </form>

          {err && <ErrorState kind="server" title={t("common.error")} description={err} />}

          {shown && shown.length === 0 && videos.length === 0 && !err && (
            <EmptyState kind="filtered_out" title={t("search.didYouMean")} />
          )}

          {videos.length > 0 && (
            <>
              <h2 className="mkt-results__heading">{t("search.videos")}</h2>
              <ul className="mkt-results" data-testid="search-videos">
                {videos.map((v) => (
                  <li key={v.video_id} className="mkt-result">
                    <Link to={`/videos/${v.video_id}`}>
                      <span className="mkt-result__title" dir="auto">
                        {v.title}
                      </span>
                      {v.duration_sec != null && (
                        <span className="mkt-result__time"> · {ts(v.duration_sec)}</span>
                      )}
                    </Link>
                  </li>
                ))}
              </ul>
            </>
          )}

          {shown && shown.length > 0 && (
            <>
              {videos.length > 0 && (
                <h2 className="mkt-results__heading">{t("search.passages")}</h2>
              )}
              <ul className="mkt-results">
                {shown.map((h) => (
                  <li key={h.segment_id} className="mkt-result">
                    <Link to={`/videos/${h.video_id}/watch?t=${Math.floor(h.start_sec)}`}>
                      <span className="mkt-result__time">
                        [{ts(h.start_sec)} {dir === "rtl" ? "←" : "→"} {ts(h.end_sec)}]
                      </span>
                      <span className="mkt-result__snippet">{renderSnippet(h.snippet)}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </>
          )}

          {youtube && (
            <YouTubeSection
              block={youtube}
              canImport={Boolean(user?.is_admin)}
              onImported={() => {}}
            />
          )}
        </div>

        <aside className="mkt-search__side" aria-label={t("search.saved")}>
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
      </div>
    </section>
  );
}

// YouTubeSection renders the "From YouTube" augmentation (Story 26.8),
// visually distinct from local results. Each card links out to YouTube
// (rel="noopener noreferrer"); a match badge links an already-in-library
// result to the local video, or offers an import to a chosen video. A
// non-empty `reason` renders a quiet inline note, never an error toast.
function YouTubeSection({
  block,
  canImport,
  onImported,
}: {
  block: YTBlock;
  /** Importing is an editor (admin) write; the server refuses others. */
  canImport: boolean;
  onImported: () => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();

  async function importTo(it: YTItem) {
    const target = window.prompt(t("search.youtube.importPrompt"));
    if (!target) return;
    try {
      await api.post(`/api/videos/${encodeURIComponent(target.trim())}/import-youtube`, {
        youtube_id: it.youtube_id,
      });
      toast.show({ message: t("search.youtube.imported"), tone: "success" });
      onImported();
    } catch (e) {
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
    }
  }

  return (
    <section className="mkt-yt-section" aria-label={t("search.youtube.heading")}>
      <h2 className="mkt-yt-section__title">{t("search.youtube.heading")}</h2>
      {block.items.length === 0 ? (
        <p className="mkt-muted">
          {block.reason && YT_REASONS.has(block.reason)
            ? t(`search.youtube.reason.${block.reason}`)
            : t("search.youtube.none")}
        </p>
      ) : (
        <ul className="mkt-yt-list" role="list">
          {block.items.map((it) => (
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
                  {it.channel}
                  {typeof it.view_count === "number" ? ` · ${n(it.view_count)}` : ""}
                </span>
              </a>
              <div className="mkt-yt-card__match">
                {it.match?.state === "in_library" && it.match.video_id ? (
                  <Link to={`/videos/${it.match.video_id}`}>
                    <Badge tone="success">{t("search.youtube.inLibrary")}</Badge>
                  </Link>
                ) : canImport ? (
                  <Button size="sm" variant="secondary" onClick={() => importTo(it)}>
                    {t("search.youtube.import")}
                  </Button>
                ) : null}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
