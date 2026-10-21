// Story 11.4 — Search interface.
//
// Real server contract (api/internal/handlers/search):
//   POST   /api/search          { q, mode, limit } -> { hits, total, ... }
//   GET    /api/search/suggest?q=  -> { suggestions: string[] }
//   POST   /api/search/save     { q, mode }
//   GET    /api/search/saved    -> { items:[{ id, q, mode }] }
//   DELETE /api/search/saved/{id}
//   GET    /api/search/youtube/status -> { available }   (yt-dlp installed?)
//   GET    /api/search/youtube?q=     -> { items, reason? }
//   POST   /api/videos/{id}/import-youtube  { youtube_id, title, channel, ... }
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
import { Modal } from "@ds/components/Modal/Modal";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { useAuth } from "../lib/auth";
import { PageHelp } from "../help/HelpLink";

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
  duration_sec?: number;
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
const YT_REASONS = new Set(["disabled", "rate_limited", "error"]);

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
      const withYt = includeYouTube && ytAvailableRef.current;
      lastSearch.current = { term: query, mode: m, yt: includeYouTube };
      setPending(true);
      setErr(null);
      if (!withYt) setYoutube(null);
      const list = <T,>(v: T[] | null | undefined) =>
        Array.isArray(v) ? v.filter((x): x is T => x != null) : [];
      try {
        const res = await api.post<SearchResponse>("/api/search", {
          q: query,
          mode: m,
          limit: SEARCH_LIMIT,
        });
        if (seq !== searchSeq.current) return;
        setHits(list(res?.hits));
        setVideos(list(res?.videos));
      } catch (e) {
        if (seq !== searchSeq.current) return;
        setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
        setHits([]);
        setVideos([]);
      } finally {
        if (seq === searchSeq.current) setPending(false);
      }
      // Story 26.8: the YouTube leg runs after the local results are on
      // screen. A yt-dlp search takes seconds and must never hold them up.
      if (!withYt) return;
      setYtPending(true);
      try {
        const block = await api.get<YTBlock>(`/api/search/youtube?q=${encodeURIComponent(query)}`);
        if (seq === searchSeq.current) {
          setYoutube({
            reason: block?.reason,
            items: list(block?.items).filter((it) => it.youtube_id),
          });
        }
      } catch {
        if (seq === searchSeq.current) setYoutube({ items: [], reason: "error" });
      } finally {
        if (seq === searchSeq.current) setYtPending(false);
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

  // Re-run the last search (after a YouTube import, so the result shows
  // its new state).
  const rerun = () => {
    const last = lastSearch.current;
    if (last) void runSearch(last.term, last.mode, last.yt);
  };

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

          {ytAvailable && withYouTube && (ytPending || youtube) && (
            <YouTubeSection
              block={youtube}
              pending={ytPending}
              canImport={Boolean(user?.is_admin)}
              localVideos={videos}
              onImported={rerun}
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
  localVideos: VideoHit[];
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
  localVideos: VideoHit[];
  onClose: () => void;
  onImported: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [find, setFind] = useState("");
  const [found, setFound] = useState<VideoHit[] | null>(null);
  const [finding, setFinding] = useState(false);
  const [chosen, setChosen] = useState<VideoHit | null>(null);
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
      const res = await api.post<SearchResponse>("/api/search", { q, mode: "fts", limit: 20 });
      setFound(res.videos ?? []);
    } catch (err) {
      setFound([]);
      toast.show({
        tone: "error",
        message: err instanceof ApiError ? err.problem.title : t("common.error"),
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
        message:
          err instanceof ApiError ? err.problem.detail || err.problem.title : t("common.error"),
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
