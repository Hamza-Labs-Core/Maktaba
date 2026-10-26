// Story 11.1 / Epic 45 — the video grid: All videos (`/videos`) and a
// library's Browse tab (`/library/:id?tab=browse`, embedded).
//
// Speaks the /api/videos contract:
//   ?library=&language=&year=&watched=&sort=&order=&limit=&cursor=
//   -> { items: Video[], next: string|null }
// sort = updated_at|created_at|published_at|title|duration_sec|plays and
// order = asc|desc are the server's (Epic 45); the cursor carries both.
// (The server field is `next`, NOT `next_cursor`.)
//
// Grid/list view persists per-user in localStorage; the sort, order and
// filters (year, watched, language) live in the URL; cursor "Load more"
// appends pages; empty states distinguish first-run (admin "Scan now") vs
// filtered-out ("Clear filters"). State badges + poster 404 fallback +
// bidi-isolated titles. "Scan now" (admins) toasts the result, follows the
// scan job (GET /api/jobs/{id}) with a progress bar, and reloads the list
// when the scan is done.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Chip } from "@ds/components/Chip/Chip";
import { Button } from "@ds/components/Button/Button";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { useToast } from "@ds/components/Toast/Toast";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { TitleRow } from "../help/HelpLink";
import { VideoPoster, ViewToggle, formatDuration, useStoredView } from "../components/VideoViews";

const VIEW_KEY = "mkt:lib:view";

interface Video {
  id: string;
  title?: string | null;
  filename: string;
  state: string;
  detected_language?: string | null;
  duration_sec?: number | null;
  size_bytes?: number;
  updated_at?: string;
}

interface VideoListResponse {
  items: Video[];
  next: string | null;
}

// The server's sort keys, each with the order it starts in (newest, longest,
// most played first; titles A to Z).
const SORTS = {
  updated_at: "desc",
  created_at: "desc",
  published_at: "desc",
  title: "asc",
  duration_sec: "desc",
  plays: "desc",
} as const;
type SortField = keyof typeof SORTS;
const SORT_LABEL: Record<SortField, string> = {
  updated_at: "library.sort.recent",
  created_at: "videosAll.sort.added",
  published_at: "videosAll.sort.published",
  title: "library.sort.title",
  duration_sec: "library.sort.duration",
  plays: "videosAll.sort.plays",
};

const WATCHED = ["unwatched", "in_progress", "watched"] as const;

// Library ids are UUIDs; the server ignores any other `library` value and
// would list every video, so a malformed id is "not found" here.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// Only states a viewer should notice get a badge. The rest
// (discovered → probed → … → ready) are processing steps, shown on the
// Processing page, not on every card.
const PROBLEM_TONE: Record<string, "warn" | "error"> = {
  missing: "error",
  corrupted: "error",
  failed: "error",
  ready_no_audio: "warn",
  superseded: "warn",
};

function StateBadge({ state }: { state: string }) {
  const { t } = useI18n();
  const tone = PROBLEM_TONE[state];
  return tone ? <Badge tone={tone}>{t(`video.state.${state}`)}</Badge> : null;
}

// How often the scan's job is re-read, and for how long at most.
export const SCAN_POLL_MS = 2000;
export const SCAN_POLL_MAX = 900; // 30 minutes

interface ScanJob {
  id: string;
  state: string;
  error?: string | null;
  estimated_remaining_sec?: number | null;
}

const SCAN_TERMINAL = new Set(["done", "failed", "cancelled", "superseded"]);

// ScanNow starts a library scan and follows it: a toast when it is queued
// (or refused), a progress bar while the job is pending or running, and a
// toast plus onDone() when it ends.
export function ScanNow({ libraryId, onDone }: { libraryId: string; onDone: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [job, setJob] = useState<ScanJob | null>(null);
  const [starting, setStarting] = useState(false);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const errMessage = (e: unknown) =>
    e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error");

  async function follow(id: string) {
    for (let i = 0; i < SCAN_POLL_MAX && alive.current; i++) {
      await new Promise((r) => setTimeout(r, SCAN_POLL_MS));
      if (!alive.current) return;
      let j: ScanJob;
      try {
        j = await api.get<ScanJob>(`/api/jobs/${encodeURIComponent(id)}`);
      } catch {
        continue; // a missed read: try again
      }
      if (!alive.current) return;
      setJob(j);
      if (SCAN_TERMINAL.has(j.state)) {
        if (j.state === "done") {
          toast.show({ tone: "success", message: t("library.scan.done") });
        } else {
          toast.show({
            tone: "error",
            message: j.error ? `${t("library.scan.failed")}: ${j.error}` : t("library.scan.failed"),
          });
        }
        setJob(null);
        onDone();
        return;
      }
    }
    if (alive.current) setJob(null);
  }

  async function start() {
    setStarting(true);
    try {
      const r = await api.post<{ job_id?: string }>(
        `/api/libraries/${encodeURIComponent(libraryId)}/scan`
      );
      toast.show({ tone: "info", message: t("library.scan.started") });
      if (r?.job_id) {
        setJob({ id: r.job_id, state: "pending" });
        void follow(r.job_id);
      }
    } catch (e) {
      toast.show({ tone: "error", message: `${t("library.scan.error")}: ${errMessage(e)}` });
    } finally {
      setStarting(false);
    }
  }

  const eta = job?.estimated_remaining_sec;
  return (
    <div className="mkt-scan-now">
      <Button onClick={() => void start()} loading={starting} disabled={starting || job !== null}>
        {job ? t("library.scan.running") : t("library.scanNow")}
      </Button>
      {job && (
        <div className="mkt-scan-now__progress" role="status">
          <ProgressBar indeterminate label={t("library.scan.running")} />
          <span className="mkt-muted">
            {t(job.state === "running" ? "library.scan.stateRunning" : "library.scan.statePending")}
            {typeof eta === "number" && eta > 0
              ? ` · ${t("library.scan.eta", { min: String(Math.max(1, Math.round(eta / 60))) })}`
              : ""}
          </span>
        </div>
      )}
    </div>
  );
}

interface LibraryBrowserProps {
  /** the library to list; defaults to the route's :libraryId, else every library */
  libraryId?: string;
  /** inside a library's page (its Browse tab): no page title of its own */
  embedded?: boolean;
  /** the years to offer in the Year filter (the library hub's); else a number field */
  years?: number[];
}

export function LibraryBrowser(props: LibraryBrowserProps = {}) {
  const params0 = useParams();
  const libraryId = props.libraryId ?? params0.libraryId;
  const { embedded, years } = props;
  const { t, n } = useI18n();
  const { user } = useAuth();
  const [params, setParams] = useSearchParams();

  const [view, setView] = useStoredView(VIEW_KEY, "grid");
  const rawSort = params.get("sort");
  // An unknown ?sort= (a stale or hand-typed link) falls back to the default.
  const sort: SortField =
    rawSort && Object.prototype.hasOwnProperty.call(SORTS, rawSort)
      ? (rawSort as SortField)
      : "updated_at";
  const rawOrder = params.get("order");
  const order: "asc" | "desc" = rawOrder === "asc" || rawOrder === "desc" ? rawOrder : SORTS[sort];
  const badLibrary = !!libraryId && !UUID.test(libraryId);
  const [reloads, setReloads] = useState(0);
  const lang = params.get("language") || "";
  const rawYear = params.get("year") || "";
  const year = /^\d{4}$/.test(rawYear) ? rawYear : "";
  const rawWatched = params.get("watched") || "";
  const watched = (WATCHED as readonly string[]).includes(rawWatched) ? rawWatched : "";

  const [videos, setVideos] = useState<Video[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const loadingMoreRef = useRef(false);
  // Bumped whenever the list starts over (new sort, library, filter or a
  // retry): a Load more answer for an older list is dropped.
  const listGen = useRef(0);
  const [err, setErr] = useState<string | null>(null);

  const buildQuery = useCallback(
    (after?: string | null) => {
      const qs = new URLSearchParams();
      qs.set("sort", sort);
      qs.set("order", order);
      qs.set("limit", "60");
      if (libraryId) qs.set("library", libraryId);
      if (lang) qs.set("language", lang);
      if (year) qs.set("year", year);
      if (watched) qs.set("watched", watched);
      if (after) qs.set("cursor", after);
      return qs.toString();
    },
    [sort, order, libraryId, lang, year, watched]
  );

  useEffect(() => {
    let cancelled = false;
    listGen.current += 1;
    loadingMoreRef.current = false;
    setLoadingMore(false);
    setVideos(null);
    setErr(null);
    setCursor(null);
    if (badLibrary) {
      setVideos([]);
      return;
    }
    api
      .get<VideoListResponse>(`/api/videos?${buildQuery()}`)
      .then((res) => {
        if (cancelled) return;
        setVideos(res.items ?? []);
        setCursor(res.next ?? null);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
        setVideos([]);
      });
    return () => {
      cancelled = true;
    };
  }, [buildQuery, t, badLibrary, reloads]);

  function loadMore() {
    // A ref, not the loadingMore state: two quick clicks run before React
    // re-renders, and both would see loadingMore === false.
    if (!cursor || loadingMoreRef.current) return;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    const gen = listGen.current;
    api
      .get<VideoListResponse>(`/api/videos?${buildQuery(cursor)}`)
      .then((res) => {
        if (gen !== listGen.current) return;
        // Skip ids already shown: a page that overlaps the last one (rows
        // re-sorted between requests) must not add duplicate cards.
        setVideos((prev) => {
          const seen = new Set((prev ?? []).map((v) => v.id));
          return [...(prev ?? []), ...(res.items ?? []).filter((v) => !seen.has(v.id))];
        });
        setCursor(res.next ?? null);
      })
      .catch((e) => {
        if (gen !== listGen.current) return;
        setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
      })
      .finally(() => {
        if (gen !== listGen.current) return;
        loadingMoreRef.current = false;
        setLoadingMore(false);
      });
  }

  function patchParams(changes: Record<string, string | null>) {
    const nextParams = new URLSearchParams(params);
    for (const [key, value] of Object.entries(changes)) {
      if (value) nextParams.set(key, value);
      else nextParams.delete(key);
    }
    setParams(nextParams, { replace: true });
  }
  const patchParam = (key: string, value: string | null) => patchParams({ [key]: value });

  const languages = useMemo(() => {
    const set = new Set<string>();
    (videos ?? []).forEach((v) => v.detected_language && set.add(v.detected_language));
    return [...set].sort();
  }, [videos]);

  const filtersActive = Boolean(lang || year || watched);
  const listName = embedded ? t("libraryPage.tab.browse") : t("videosAll.title");

  const toolbar = (
    <div className="mkt-toolbar" role="toolbar" aria-label={t("library.sort")}>
      <label className="mkt-field">
        <span className="mkt-field__label">{t("library.sort")}</span>
        <select
          value={sort}
          // a new sort starts in its own order
          onChange={(e) => patchParams({ sort: e.target.value, order: null })}
          aria-label={t("library.sort")}
        >
          {(Object.keys(SORTS) as SortField[]).map((s) => (
            <option key={s} value={s}>
              {t(SORT_LABEL[s])}
            </option>
          ))}
        </select>
      </label>
      <button
        type="button"
        className="mkt-btn mkt-btn--ghost"
        aria-label={order === "desc" ? t("library.order.asc") : t("library.order.desc")}
        onClick={() => patchParam("order", order === "desc" ? "asc" : "desc")}
      >
        {order === "desc" ? "↓" : "↑"}
      </button>
      <label className="mkt-field">
        <span className="mkt-field__label">{t("videosAll.filter.watched")}</span>
        <select
          value={watched}
          onChange={(e) => patchParam("watched", e.target.value || null)}
          aria-label={t("videosAll.filter.watched")}
        >
          <option value="">{t("library.filter.all")}</option>
          {WATCHED.map((w) => (
            <option key={w} value={w}>
              {t(`videosAll.watched.${w}`)}
            </option>
          ))}
        </select>
      </label>
      <label className="mkt-field">
        <span className="mkt-field__label">{t("videosAll.filter.year")}</span>
        {years && years.length > 0 ? (
          <select
            value={year}
            onChange={(e) => patchParam("year", e.target.value || null)}
            aria-label={t("videosAll.filter.year")}
          >
            <option value="">{t("library.filter.all")}</option>
            {/* a year from a link that the hub does not list still shows */}
            {[...new Set([...(year ? [Number(year)] : []), ...years])]
              .sort((a, b) => b - a)
              .map((y) => (
                <option key={y} value={String(y)}>
                  {n(y, { useGrouping: false })}
                </option>
              ))}
          </select>
        ) : (
          <input
            type="number"
            inputMode="numeric"
            min={1900}
            max={2100}
            className="mkt-year-input"
            defaultValue={year}
            key={year}
            aria-label={t("videosAll.filter.year")}
            placeholder={t("library.filter.all")}
            onBlur={(e) => {
              const v = e.target.value.trim();
              if (v !== year && (v === "" || /^\d{4}$/.test(v))) patchParam("year", v || null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") (e.target as HTMLInputElement).blur();
            }}
          />
        )}
      </label>
      <ViewToggle view={view} onChange={setView} />
    </div>
  );

  return (
    <section className={embedded ? "mkt-library mkt-library--embedded" : "mkt-page mkt-library"}>
      {embedded ? (
        toolbar
      ) : (
        <header className="mkt-page__header">
          <TitleRow>
            <h1>{t("videosAll.title")}</h1>
          </TitleRow>
          {toolbar}
        </header>
      )}

      {languages.length > 0 && (
        <div className="mkt-chips" role="group" aria-label={t("library.filter.language")}>
          {languages.map((l) => (
            <button
              key={l}
              type="button"
              className="mkt-chip-btn"
              onClick={() => patchParam("language", lang === l ? null : l)}
            >
              <Chip selected={lang === l}>{l}</Chip>
            </button>
          ))}
        </div>
      )}

      {videos === null && (
        <ul className={view === "grid" ? "mkt-grid" : "mkt-list"} aria-busy="true">
          {Array.from({ length: 6 }, (_, i) => (
            <li key={i} className="mkt-card">
              <Skeleton variant="rect" height={120} />
              <Skeleton variant="text" lines={2} />
            </li>
          ))}
        </ul>
      )}

      {badLibrary && <ErrorState kind="not_found" title={t("library.notFound")} />}

      {err && (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={err}
          action={<Button onClick={() => setReloads((x) => x + 1)}>{t("common.retry")}</Button>}
        />
      )}

      {videos !== null && videos.length === 0 && !err && !badLibrary && (
        <EmptyState
          kind={filtersActive ? "filtered_out" : "first_run"}
          title={filtersActive ? t("library.empty.filtered") : t("library.empty.title")}
          description={filtersActive ? undefined : t("library.empty.desc")}
          action={
            filtersActive ? (
              <Button
                variant="secondary"
                onClick={() => patchParams({ language: null, year: null, watched: null })}
              >
                {t("common.clearFilters")}
              </Button>
            ) : user?.is_admin && libraryId ? (
              <ScanNow libraryId={libraryId} onDone={() => setReloads((x) => x + 1)} />
            ) : undefined
          }
        />
      )}

      {videos !== null && videos.length > 0 && view === "grid" && (
        <ul className="mkt-grid">
          {videos.map((v) => (
            <li key={v.id} className="mkt-card">
              <Link to={`/videos/${v.id}`}>
                <VideoPoster videoId={v.id} />
                <div className="mkt-card__title" dir="auto">
                  {v.title || v.filename}
                </div>
                <div className="mkt-card__meta">
                  {typeof v.duration_sec === "number" && (
                    <span>{formatDuration(v.duration_sec)}</span>
                  )}
                  {v.detected_language && <span> · {v.detected_language}</span>}
                </div>
                <StateBadge state={v.state} />
              </Link>
            </li>
          ))}
        </ul>
      )}

      {videos !== null && videos.length > 0 && view === "list" && (
        <table className="mkt-table" aria-label={listName}>
          <thead>
            <tr>
              <th>{t("library.col.title")}</th>
              <th>{t("library.col.duration")}</th>
              <th>{t("library.col.language")}</th>
              <th>{t("library.col.state")}</th>
            </tr>
          </thead>
          <tbody>
            {videos.map((v) => (
              <tr key={v.id}>
                <td dir="auto">
                  <Link to={`/videos/${v.id}`}>{v.title || v.filename}</Link>
                </td>
                <td>{typeof v.duration_sec === "number" ? formatDuration(v.duration_sec) : "—"}</td>
                <td>{v.detected_language ?? "—"}</td>
                <td>{PROBLEM_TONE[v.state] ? <StateBadge state={v.state} /> : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {cursor && (
        <div className="mkt-loadmore">
          <Button variant="secondary" loading={loadingMore} onClick={loadMore}>
            {t("common.loadMore")} {n((videos ?? []).length)}
          </Button>
        </div>
      )}
    </section>
  );
}
