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
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Chip } from "@ds/components/Chip/Chip";
import { Button } from "@ds/components/Button/Button";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";

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
const SERVER_SORT: Record<SortField, string> = {
  updated_at: "updated_at",
  title: "title",
  duration_sec: "updated_at",
};

const BADGE_TONE: Record<string, "neutral" | "accent" | "success" | "warn" | "error"> = {
  ready: "success",
  processing: "accent",
  pending: "neutral",
  missing: "error",
  ready_no_audio: "warn",
  superseded: "warn",
  corrupted: "error",
  failed: "error",
};

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
  const sort = (params.get("sort") as SortField) || "updated_at";
  const order = params.get("order") === "asc" ? "asc" : "desc";
  const lang = params.get("language") || "";

  const [videos, setVideos] = useState<Video[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
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
    setVideos(null);
    setErr(null);
    setCursor(null);
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
  }, [buildQuery, t]);

  function loadMore() {
    if (!cursor || loadingMore) return;
    setLoadingMore(true);
    api
      .get<VideoListResponse>(`/api/videos?${buildQuery(cursor)}`)
      .then((res) => {
        setVideos((prev) => [...(prev ?? []), ...(res.items ?? [])]);
        setCursor(res.next ?? null);
      })
      .catch((e) => setErr(e instanceof ApiError ? e.problem.title : t("common.error")))
      .finally(() => setLoadingMore(false));
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
      arr.sort((a, b) => (a.title || a.filename).localeCompare(b.title || b.filename, locale));
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

      {err && (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={err}
          action={
            <Button onClick={() => patchParam("_r", String(Date.now()))}>
              {t("common.retry")}
            </Button>
          }
        />
      )}

      {sorted !== null && sorted.length === 0 && !err && (
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
              <Button
                onClick={() => void api.post(`/api/libraries/${libraryId}/scan`).catch(() => {})}
              >
                {t("library.scanNow")}
              </Button>
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
                <Badge tone={BADGE_TONE[v.state] ?? "neutral"}>{v.state}</Badge>
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
                <td>
                  <Badge tone={BADGE_TONE[v.state] ?? "neutral"}>{v.state}</Badge>
                </td>
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
