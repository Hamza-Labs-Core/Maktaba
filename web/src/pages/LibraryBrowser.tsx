// Story 11.1 — Library browser (grid/list, sort, filter).
//
// Renders a list of videos pulled from `/api/videos`. Phase 10
// scaffolds the layout, sort/filter controls, and empty/loading
// states. Story-specific work (poster sprite, virtualisation, infinite
// scroll) lands in later iterations of Epic 11.
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";

type View = "grid" | "list";
type Sort = "recent" | "title" | "duration";

interface Video {
  id: string;
  title: string;
  duration_sec?: number;
  poster_url?: string;
  library_id?: string;
}

interface VideoListResponse {
  items: Video[];
  next_cursor?: string | null;
}

export function LibraryBrowser() {
  const { libraryId } = useParams();
  const { t } = useI18n();
  const [view, setView] = useState<View>("grid");
  const [sort, setSort] = useState<Sort>("recent");
  const [videos, setVideos] = useState<Video[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    setVideos(null);
    setErr(null);
    const qs = new URLSearchParams();
    qs.set("sort", sort);
    if (libraryId) qs.set("library_id", libraryId);
    api
      .get<VideoListResponse>(`/api/videos?${qs.toString()}`)
      .then((res) => setVideos(res.items ?? []))
      .catch((e) => {
        if (e instanceof ApiError) setErr(e.problem.title);
        else setErr(t("common.error"));
        setVideos([]);
      });
  }, [libraryId, sort, t]);

  return (
    <section className="mkt-page mkt-library">
      <header className="mkt-page__header">
        <h1>{t("nav.library")}</h1>
        <div className="mkt-toolbar" role="toolbar">
          <select value={sort} onChange={(e) => setSort(e.target.value as Sort)} aria-label="Sort">
            <option value="recent">Recent</option>
            <option value="title">Title</option>
            <option value="duration">Duration</option>
          </select>
          <div className="mkt-segmented" role="radiogroup" aria-label="View">
            <button
              type="button"
              role="radio"
              aria-checked={view === "grid"}
              onClick={() => setView("grid")}
            >
              Grid
            </button>
            <button
              type="button"
              role="radio"
              aria-checked={view === "list"}
              onClick={() => setView("list")}
            >
              List
            </button>
          </div>
        </div>
      </header>
      {videos === null && <p>{t("common.loading")}</p>}
      {err && (
        <div className="mkt-alert" role="alert">
          {err}
        </div>
      )}
      {videos !== null && videos.length === 0 && !err && (
        <p className="mkt-empty">{t("common.empty")}</p>
      )}
      {videos !== null && videos.length > 0 && (
        <ul className={view === "grid" ? "mkt-grid" : "mkt-list"}>
          {videos.map((v) => (
            <li key={v.id} className="mkt-card">
              <Link to={`/videos/${v.id}`}>
                <div
                  className="mkt-card__poster"
                  style={v.poster_url ? { backgroundImage: `url(${v.poster_url})` } : undefined}
                  aria-hidden
                />
                <div className="mkt-card__title">{v.title}</div>
                {typeof v.duration_sec === "number" && (
                  <div className="mkt-card__meta">{formatDuration(v.duration_sec)}</div>
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
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
