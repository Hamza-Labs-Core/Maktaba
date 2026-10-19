// Story 11.1 — Library browser.
//
// Speaks the real /api/videos contract:
//   ?library=&language=&sort=(updated_at|created_at|title)&limit=&cursor=
//   -> { items: Video[], next: string|null }
// (the server field is `next`, NOT `next_cursor`; the old code's
// `next_cursor` typo meant pagination never advanced.)
//
// Grid/list view persists per-user in localStorage; language filter
// chips are URL-encoded; cursor "Load more" appends pages; empty states
// distinguish first-run (admin "Scan now") vs filtered-out ("Clear
// filters"). State badges + poster 404 fallback + bidi-isolated titles.
// "Scan now" (admins) toasts the result, follows the scan job
// (GET /api/jobs/{id}) with a progress bar, and reloads the list when the
// scan is done.
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
import { WhatsOnNow } from "../components/WhatsOnNow";

type View = "grid" | "list";
type SortField = "updated_at" | "title" | "duration_sec";
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

// Server only sorts on updated_at|created_at|title; `duration_sec` is a
// client-side sort over the loaded page (documented limitation: the
// list API has no duration sort key).
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const SERVER_SORT: Record<SortField, string> = {
  updated_at: "updated_at",
  title: "title",
  duration_sec: "updated_at",
};

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

function readView(): View {
  try {
    const v = localStorage.getItem(VIEW_KEY);
    if (v === "grid" || v === "list") return v;
  } catch {
    /* ignored */
  }
  return "grid";
}

export function LibraryBrowser() {
  const { libraryId } = useParams();
  const { t, n, locale } = useI18n();
  const { user } = useAuth();
  const [params, setParams] = useSearchParams();

  const [view, setView] = useState<View>(readView);
  const rawSort = params.get("sort");
  // An unknown ?sort= (a stale or hand-typed link) falls back to the default.
  const sort: SortField =
    rawSort && Object.prototype.hasOwnProperty.call(SERVER_SORT, rawSort)
      ? (rawSort as SortField)
      : "updated_at";
  // Library ids are UUIDs; the server ignores any other `library` value and
  // would list every video, so a malformed id is "not found" here.
  const badLibrary = !!libraryId && !UUID.test(libraryId);
  const [reloads, setReloads] = useState(0);
  const order = params.get("order") === "asc" ? "asc" : "desc";
  const lang = params.get("language") || "";

  const [videos, setVideos] = useState<Video[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const loadingMoreRef = useRef(false);
  // Bumped whenever the list starts over (new sort, library, language or a
  // retry): a Load more answer for an older list is dropped.
  const listGen = useRef(0);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    try {
      localStorage.setItem(VIEW_KEY, view);
    } catch {
      /* ignored */
    }
  }, [view]);

  const buildQuery = useCallback(
    (after?: string | null) => {
      const qs = new URLSearchParams();
      qs.set("sort", SERVER_SORT[sort]);
      qs.set("limit", "60");
      if (libraryId) qs.set("library", libraryId);
      if (lang) qs.set("language", lang);
      if (after) qs.set("cursor", after);
      return qs.toString();
    },
    [sort, libraryId, lang]
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

  function patchParam(key: string, value: string | null) {
    const nextParams = new URLSearchParams(params);
    if (value) nextParams.set(key, value);
    else nextParams.delete(key);
    setParams(nextParams, { replace: true });
  }

  const sorted = useMemo(() => {
    if (!videos) return videos;
    const arr = videos.slice();
    if (sort === "duration_sec") {
      arr.sort((a, b) => (a.duration_sec ?? 0) - (b.duration_sec ?? 0));
    } else if (sort === "title") {
      arr.sort((a, b) =>
        (a.title || a.filename || "").localeCompare(b.title || b.filename || "", locale)
      );
    }
    if (order === "desc") arr.reverse();
    return arr;
  }, [videos, sort, order, locale]);

  const languages = useMemo(() => {
    const set = new Set<string>();
    (videos ?? []).forEach((v) => v.detected_language && set.add(v.detected_language));
    return [...set].sort();
  }, [videos]);

  const filtersActive = Boolean(lang);

  return (
    <section className="mkt-page mkt-library">
      <header className="mkt-page__header">
        <h1>{t("nav.library")}</h1>
        <div className="mkt-toolbar" role="toolbar" aria-label={t("library.sort")}>
          <label className="mkt-field">
            <span className="mkt-field__label">{t("library.sort")}</span>
            <select
              value={sort}
              onChange={(e) => patchParam("sort", e.target.value)}
              aria-label={t("library.sort")}
            >
              <option value="updated_at">{t("library.sort.recent")}</option>
              <option value="title">{t("library.sort.title")}</option>
              <option value="duration_sec">{t("library.sort.duration")}</option>
            </select>
          </label>
          <button
            type="button"
            className="mkt-btn mkt-btn--ghost"
            aria-label={order === "desc" ? t("library.order.asc") : t("library.order.desc")}
            onClick={() => patchParam("order", order === "desc" ? "asc" : null)}
          >
            {order === "desc" ? "↓" : "↑"}
          </button>
          <div className="mkt-segmented" role="radiogroup" aria-label={t("library.view")}>
            <button
              type="button"
              role="radio"
              aria-checked={view === "grid"}
              onClick={() => setView("grid")}
            >
              {t("library.view.grid")}
            </button>
            <button
              type="button"
              role="radio"
              aria-checked={view === "list"}
              onClick={() => setView("list")}
            >
              {t("library.view.list")}
            </button>
          </div>
        </div>
      </header>

      <WhatsOnNow />

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
          action={<Button onClick={() => setReloads((n) => n + 1)}>{t("common.retry")}</Button>}
        />
      )}

      {sorted !== null && sorted.length === 0 && !err && !badLibrary && (
        <EmptyState
          kind={filtersActive ? "filtered_out" : "first_run"}
          title={filtersActive ? t("library.empty.filtered") : t("library.empty.title")}
          description={filtersActive ? undefined : t("library.empty.desc")}
          action={
            filtersActive ? (
              <Button variant="secondary" onClick={() => patchParam("language", null)}>
                {t("common.clearFilters")}
              </Button>
            ) : user?.is_admin && libraryId ? (
              <ScanNow libraryId={libraryId} onDone={() => setReloads((n) => n + 1)} />
            ) : undefined
          }
        />
      )}

      {sorted !== null && sorted.length > 0 && view === "grid" && (
        <ul className="mkt-grid">
          {sorted.map((v) => (
            <li key={v.id} className="mkt-card">
              <Link to={`/videos/${v.id}`}>
                <Poster video={v} />
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

      {sorted !== null && sorted.length > 0 && view === "list" && (
        <table className="mkt-table" aria-label={t("nav.library")}>
          <thead>
            <tr>
              <th>{t("library.col.title")}</th>
              <th>{t("library.col.duration")}</th>
              <th>{t("library.col.language")}</th>
              <th>{t("library.col.state")}</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((v) => (
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
            {t("common.loadMore")} {n((sorted ?? []).length)}
          </Button>
        </div>
      )}
    </section>
  );
}

function Poster({ video }: { video: Video }) {
  const [broken, setBroken] = useState(false);
  const url = `/api/videos/${video.id}/poster`;
  if (broken) {
    return <div className="mkt-card__poster mkt-card__poster--fallback" aria-hidden />;
  }
  return (
    <img
      className="mkt-card__poster"
      src={url}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  );
}

function formatDuration(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  const mm = m.toString().padStart(2, "0");
  const ss = s.toString().padStart(2, "0");
  return h > 0 ? `${h}:${mm}:${ss}` : `${m}:${ss}`;
}
