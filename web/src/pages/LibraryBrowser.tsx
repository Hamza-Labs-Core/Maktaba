// Story 11.1 / Epic 45 — the video grid: All videos (`/videos`) and a
// library's Browse tab (`/library/:id?tab=browse`, embedded).
//
// Speaks the /api/videos contract:
//   ?library=&language=&year=&watched=&sort=&order=&limit=&cursor=
//   -> { items: Video[], next: string|null }
// sort = updated_at|created_at|published_at|title|duration_sec|plays and
// order = asc|desc are the server's (Epic 45); the cursor carries both, and
// the server refuses (400) a cursor minted for another sort or order, so a
// new sort, order or filter always starts over from the first page.
// (The server field is `next`, NOT `next_cursor`.)
//
// Phase 2 (stories 45.8, 45.11 to 45.13, 45.16): a search box (q), Tag,
// Speaker and Language filters from GET /api/videos/facets, a Favorites
// toggle, the A to Z jump bar (letter=, under the title sort), watched ticks,
// progress bars and hearts on the cards, Play all / Shuffle (lib/playQueue)
// and Select: check cards, then mark them watched or not, (un)favorite them or
// add them to a playlist (lib/me).
//
// Phase 3 (story 45.20): Length, Added and Resolution filters (bands the
// server defines; the facets say which have videos). Story 45.17, admins on a
// library's grid: Add to collection (a selection) and Save as collection (the
// filters and sort as a smart collection); with ?collection=<id> (Edit filters
// on a smart collection's page) the button saves them to that collection.
//
// Grid/list view persists per-user in localStorage; the sort, order and
// filters (year, watched, language, q, tag, speaker, favorite, letter,
// duration, added, resolution) live in the URL; cursor "Load more"
// appends pages; empty states distinguish first-run (admin "Scan now") vs
// filtered-out ("Clear filters"). State badges + poster 404 fallback +
// bidi-isolated titles. "Scan now" (admins) toasts the result, follows the
// scan job (GET /api/jobs/{id}) with a progress bar, and reloads the list
// when the scan is done.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
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
import { AddToPlaylist } from "../components/AddToPlaylist";
import { AddToCollection } from "../components/AddToCollection";
import { Modal } from "@ds/components/Modal/Modal";
import {
  COLLECTION_NAME_MAX,
  collectionPath,
  createCollection,
  pickFilters,
  saveCollectionFilters,
} from "../lib/collections";
import { EMPTY_FACETS, fetchFacets, markWatched, setFavorite, type Facets } from "../lib/me";
import { startQueueFromQuery, watchPath } from "../lib/playQueue";
import { videosUrl } from "../lib/browse";

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
  /** the file's mtime (a download's upload date) */
  published_at?: string | null;
  /** everyone's plays; only under sort=plays */
  plays?: number | null;
  /** the caller's own playback (Phase 2) */
  playback_state?: { position_sec: number; completed: boolean; updated_at?: string } | null;
  /** one of the caller's favorites (Phase 2) */
  favorite?: boolean;
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

// Story 45.20's bands, in the server's order (videos/bands.go).
export const BAND_FILTERS = {
  duration: ["short", "medium", "long"],
  added: ["week", "month", "year"],
  resolution: ["sd", "hd", "fhd", "uhd"],
} as const;
type BandKey = keyof typeof BAND_FILTERS;
const BAND_FACET: Record<BandKey, "durations" | "added" | "resolutions"> = {
  duration: "durations",
  added: "added",
  resolution: "resolutions",
};

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
// toast plus onDone() when it ends. A scan already running (jobId, from
// GET /api/libraries/manage after a reload) is followed the same way.
export function ScanNow({
  libraryId,
  onDone,
  jobId,
}: {
  libraryId: string;
  onDone: () => void;
  jobId?: string | null;
}) {
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

  useEffect(() => {
    if (!jobId) return;
    setJob({ id: jobId, state: "running" });
    void follow(jobId);
    // follow once per job id handed in (follow itself is rebuilt every render)
  }, [jobId]);

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

// The filters kept in the URL; Clear filters removes them all.
export const FILTER_KEYS = [
  "language",
  "year",
  "watched",
  "q",
  "tag",
  "speaker",
  "favorite",
  "letter",
  "duration",
  "added",
  "resolution",
];

/** How far into a grid item the viewer is (0..1), null when nothing to show. */
function progressOf(v: Video): number | null {
  const ps = v.playback_state;
  if (!ps || ps.completed) return null;
  const dur = v.duration_sec ?? 0;
  if (!(ps.position_sec > 0) || dur <= 0) return null;
  return Math.min(1, ps.position_sec / dur);
}

export function LibraryBrowser(props: LibraryBrowserProps = {}) {
  const params0 = useParams();
  const libraryId = props.libraryId ?? params0.libraryId;
  const { embedded, years } = props;
  const { t, n, formatDate } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
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
  const q = (params.get("q") || "").trim();
  const rawTag = params.get("tag") || "";
  const tag = UUID.test(rawTag) ? rawTag : "";
  const speaker = params.get("speaker") || "";
  const favorite = params.get("favorite") === "true";
  const rawLetter = params.get("letter") || "";
  // one character (a letter, or "#" for the rest)
  const letter = [...rawLetter].length === 1 ? rawLetter : "";
  const bandValue = (key: BandKey) => {
    const v = params.get(key) || "";
    return (BAND_FILTERS[key] as readonly string[]).includes(v) ? v : "";
  };
  const duration = bandValue("duration");
  const added = bandValue("added");
  const resolution = bandValue("resolution");

  const [videos, setVideos] = useState<Video[] | null>(null);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const loadingMoreRef = useRef(false);
  // Bumped whenever the list starts over (new sort, library, filter or a
  // retry): a Load more answer for an older list is dropped.
  const listGen = useRef(0);
  const [err, setErr] = useState<string | null>(null);
  const [facets, setFacets] = useState<Facets>(EMPTY_FACETS);
  // Select mode: the checked videos, in the order they were checked.
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [playlistOpen, setPlaylistOpen] = useState(false);
  const [collectionOpen, setCollectionOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [saveName, setSaveName] = useState("");
  const [saving, setSaving] = useState(false);
  const editCollection = params.get("collection") || "";
  const curator = !!user?.is_admin && !!libraryId && !badLibrary;
  const [starting, setStarting] = useState(false);

  // The list's filters and sort, without paging: Play all reads it too.
  const filterQuery = useMemo(() => {
    const qs = new URLSearchParams();
    qs.set("sort", sort);
    qs.set("order", order);
    if (libraryId) qs.set("library", libraryId);
    if (lang) qs.set("language", lang);
    if (year) qs.set("year", year);
    if (watched) qs.set("watched", watched);
    if (q) qs.set("q", q);
    if (tag) qs.set("tag", tag);
    if (speaker) qs.set("speaker", speaker);
    if (favorite) qs.set("favorite", "true");
    if (letter) qs.set("letter", letter);
    if (duration) qs.set("duration", duration);
    if (added) qs.set("added", added);
    if (resolution) qs.set("resolution", resolution);
    return qs.toString();
  }, [
    sort,
    order,
    libraryId,
    lang,
    year,
    watched,
    q,
    tag,
    speaker,
    favorite,
    letter,
    duration,
    added,
    resolution,
  ]);

  const buildQuery = useCallback(
    (after?: string | null) => {
      const qs = new URLSearchParams(filterQuery);
      qs.set("limit", "60");
      if (after) qs.set("cursor", after);
      return qs.toString();
    },
    [filterQuery]
  );

  useEffect(() => {
    let cancelled = false;
    listGen.current += 1;
    loadingMoreRef.current = false;
    setLoadingMore(false);
    setVideos(null);
    setErr(null);
    setCursor(null);
    setSelected([]);
    if (badLibrary) {
      setVideos([]);
      return;
    }
    api
      .get<VideoListResponse>(videosUrl(buildQuery()))
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

  // What the filters offer (the library's, or every visible library's). An
  // older server without the route offers nothing more than before.
  useEffect(() => {
    let cancelled = false;
    setFacets(EMPTY_FACETS);
    if (badLibrary) return;
    fetchFacets(libraryId || undefined)
      .then((f) => !cancelled && setFacets(f))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [libraryId, badLibrary, reloads]);

  function loadMore() {
    // A ref, not the loadingMore state: two quick clicks run before React
    // re-renders, and both would see loadingMore === false.
    if (!cursor || loadingMoreRef.current) return;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    const gen = listGen.current;
    api
      .get<VideoListResponse>(videosUrl(buildQuery(cursor)))
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
  const clearFilters = () => patchParams(Object.fromEntries(FILTER_KEYS.map((k) => [k, null])));

  // The facets' languages; before they come (or from an older server), the
  // languages of the videos shown. A language picked by a link always shows.
  const languages = useMemo(() => {
    const set = new Set<string>(facets.languages.map((l) => l.code));
    if (set.size === 0) {
      (videos ?? []).forEach((v) => v.detected_language && set.add(v.detected_language));
    }
    if (lang) set.add(lang);
    return [...set].sort();
  }, [facets.languages, videos, lang]);

  const filtersActive = Boolean(
    lang ||
      year ||
      watched ||
      q ||
      tag ||
      speaker ||
      favorite ||
      letter ||
      duration ||
      added ||
      resolution
  );
  const bandValues: Record<BandKey, string> = { duration, added, resolution };
  const listName = embedded ? t("libraryPage.tab.browse") : t("videosAll.title");
  const speakers = facets.speakers;
  const tags = facets.tags;
  const showJump = sort === "title" && facets.letters.length > 0;

  async function play(shuffle: boolean) {
    setStarting(true);
    try {
      const first = await startQueueFromQuery(filterQuery, shuffle);
      if (first) navigate(watchPath(first), { state: { autoplay: true } });
      else toast.show({ tone: "info", message: t("browse.play.nothing") });
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
      });
    } finally {
      setStarting(false);
    }
  }

  // a saved filter is the grid's filters and sort, as the URL has them
  async function saveFilters() {
    if (!libraryId) return;
    // the sort and order the grid shows, even when the URL leaves them to the
    // defaults: a saved filter is never empty
    const filters = { ...pickFilters(params), sort, order };
    setSaving(true);
    try {
      if (editCollection) {
        await saveCollectionFilters(editCollection, filters);
        toast.show({ tone: "success", message: t("collections.filtersSaved") });
        navigate(`/collections/${encodeURIComponent(editCollection)}`);
        return;
      }
      const nm = saveName.trim();
      if (!nm) return;
      const c = await createCollection(libraryId, nm, filters);
      setSaveOpen(false);
      setSaveName("");
      toast.show({ tone: "success", message: t("collections.saved", { name: c.name }) });
      navigate(collectionPath(c));
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("collections.createError")}: ${
          e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error")
        }`,
      });
    } finally {
      setSaving(false);
    }
  }

  function toggleSelected(id: string) {
    setSelected((s) => (s.includes(id) ? s.filter((x) => x !== id) : [...s, id]));
  }

  function stopSelecting() {
    setSelecting(false);
    setSelected([]);
  }

  async function bulk(kind: "watched" | "unwatched" | "favorite" | "unfavorite") {
    const ids = selected;
    if (ids.length === 0) return;
    setBulkBusy(true);
    try {
      if (kind === "watched" || kind === "unwatched") {
        const done = kind === "watched";
        await markWatched(ids, done);
        const now = new Date().toISOString();
        setVideos((vs) =>
          (vs ?? []).map((v) =>
            ids.includes(v.id)
              ? {
                  ...v,
                  playback_state: {
                    position_sec: 0,
                    completed: done,
                    updated_at: v.playback_state?.updated_at ?? now,
                  },
                }
              : v
          )
        );
      } else {
        const fav = kind === "favorite";
        await setFavorite(ids, fav);
        setVideos((vs) =>
          (vs ?? []).map((v) => (ids.includes(v.id) ? { ...v, favorite: fav } : v))
        );
      }
      toast.show({ tone: "success", message: t(`browse.bulk.done.${kind}`, { n: n(ids.length) }) });
      setSelected([]);
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("browse.bulk.error")}: ${
          e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error")
        }`,
      });
    } finally {
      setBulkBusy(false);
    }
  }

  const toolbar = (
    <div className="mkt-toolbar" role="toolbar" aria-label={t("library.sort")}>
      <form
        className="mkt-browse-search"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          const input = e.currentTarget.elements.namedItem("q") as HTMLInputElement | null;
          const v = (input?.value ?? "").trim();
          if (v !== q) patchParam("q", v || null);
        }}
      >
        <input
          type="search"
          name="q"
          dir="auto"
          key={q}
          defaultValue={q}
          aria-label={libraryId ? t("browse.search.library") : t("browse.search.all")}
          placeholder={libraryId ? t("browse.search.library") : t("browse.search.all")}
        />
        <Button type="submit" variant="secondary" size="sm">
          {t("browse.search.go")}
        </Button>
      </form>
      <label className="mkt-field">
        <span className="mkt-field__label">{t("library.sort")}</span>
        <select
          value={sort}
          // a new sort starts in its own order; the jump bar is the title sort's
          onChange={(e) => patchParams({ sort: e.target.value, order: null, letter: null })}
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
      {(tags.length > 0 || tag) && (
        <label className="mkt-field">
          <span className="mkt-field__label">{t("browse.filter.tag")}</span>
          <select
            value={tag}
            onChange={(e) => patchParam("tag", e.target.value || null)}
            aria-label={t("browse.filter.tag")}
          >
            <option value="">{t("library.filter.all")}</option>
            {tag && !tags.some((x) => x.id === tag) && <option value={tag}>{tag}</option>}
            {tags.map((x) => (
              <option key={x.id} value={x.id}>
                {x.name} ({n(x.videos)})
              </option>
            ))}
          </select>
        </label>
      )}
      {(speakers.length > 0 || speaker) && (
        <label className="mkt-field">
          <span className="mkt-field__label">{t("browse.filter.speaker")}</span>
          <select
            value={speaker}
            onChange={(e) => patchParam("speaker", e.target.value || null)}
            aria-label={t("browse.filter.speaker")}
          >
            <option value="">{t("library.filter.all")}</option>
            {speaker && !speakers.some((x) => x.name === speaker) && (
              <option value={speaker}>{speaker}</option>
            )}
            {speakers.map((x) => (
              <option key={x.name} value={x.name}>
                {x.name} ({n(x.videos)})
              </option>
            ))}
          </select>
        </label>
      )}
      {(Object.keys(BAND_FILTERS) as BandKey[]).map((key) => {
        const offered = facets[BAND_FACET[key]];
        const current = bandValues[key];
        // a filter whose bands have no videos is left out, unless a link set it
        if (offered.length === 0 && !current) return null;
        const count = new Map(offered.map((b) => [b.value, b.videos]));
        return (
          <label key={key} className="mkt-field">
            <span className="mkt-field__label">{t(`browse.filter.${key}`)}</span>
            <select
              value={current}
              onChange={(e) => patchParam(key, e.target.value || null)}
              aria-label={t(`browse.filter.${key}`)}
            >
              <option value="">{t("library.filter.all")}</option>
              {BAND_FILTERS[key]
                .filter((b) => count.has(b) || b === current)
                .map((b) => (
                  <option key={b} value={b}>
                    {t(`browse.${key}.${b}`)}
                    {count.has(b) ? ` (${n(count.get(b) ?? 0)})` : ""}
                  </option>
                ))}
            </select>
          </label>
        );
      })}
      <button
        type="button"
        className="mkt-btn mkt-btn--ghost"
        aria-pressed={favorite}
        onClick={() => patchParam("favorite", favorite ? null : "true")}
      >
        <span aria-hidden="true">{favorite ? "♥" : "♡"}</span> {t("browse.filter.favorites")}
      </button>
      <ViewToggle view={view} onChange={setView} />
    </div>
  );

  const actions = (
    <div className="mkt-browse-actions">
      <Button
        variant="primary"
        size="sm"
        onClick={() => void play(false)}
        disabled={starting || !videos || videos.length === 0}
      >
        ▶ {t("browse.playAll")}
      </Button>
      <Button
        variant="secondary"
        size="sm"
        onClick={() => void play(true)}
        disabled={starting || !videos || videos.length === 0}
      >
        ⇄ {t("browse.shuffle")}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        aria-pressed={selecting}
        onClick={() => (selecting ? stopSelecting() : setSelecting(true))}
        disabled={!videos || videos.length === 0}
      >
        {selecting ? t("browse.select.done") : t("browse.select")}
      </Button>
      {curator && (
        <Button
          variant="ghost"
          size="sm"
          loading={saving}
          // favorites are the viewer's own: a collection cannot keep them
          disabled={saving || favorite}
          title={favorite ? t("collections.noFavorites") : undefined}
          onClick={() => (editCollection ? void saveFilters() : setSaveOpen(true))}
        >
          {editCollection ? t("collections.saveFiltersTo") : t("collections.saveAs")}
        </Button>
      )}
    </div>
  );

  const bulkBar = selecting && (
    <div className="mkt-bulkbar" role="region" aria-label={t("browse.select.region")}>
      <span className="mkt-bulkbar__count" aria-live="polite">
        {t("browse.select.count", { n: n(selected.length) })}
      </span>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setSelected((videos ?? []).map((v) => v.id))}
        disabled={bulkBusy}
      >
        {t("browse.select.all")}
      </Button>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setSelected([])}
        disabled={bulkBusy || selected.length === 0}
      >
        {t("browse.select.none")}
      </Button>
      <span className="mkt-bulkbar__actions">
        <Button
          size="sm"
          variant="secondary"
          disabled={bulkBusy || selected.length === 0}
          onClick={() => void bulk("watched")}
        >
          {t("browse.bulk.watched")}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          disabled={bulkBusy || selected.length === 0}
          onClick={() => void bulk("unwatched")}
        >
          {t("browse.bulk.unwatched")}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          disabled={bulkBusy || selected.length === 0}
          onClick={() => void bulk("favorite")}
        >
          {t("browse.bulk.favorite")}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          disabled={bulkBusy || selected.length === 0}
          onClick={() => void bulk("unfavorite")}
        >
          {t("browse.bulk.unfavorite")}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          disabled={bulkBusy || selected.length === 0}
          onClick={() => setPlaylistOpen(true)}
        >
          {t("browse.bulk.playlist")}
        </Button>
        {curator && (
          <Button
            size="sm"
            variant="secondary"
            disabled={bulkBusy || selected.length === 0}
            onClick={() => setCollectionOpen(true)}
          >
            {t("collections.bulkAdd")}
          </Button>
        )}
      </span>
    </div>
  );

  const jumpBar = showJump && (
    <nav className="mkt-jumpbar" aria-label={t("browse.jump")}>
      <ul>
        <li>
          <button
            type="button"
            className="mkt-jumpbar__btn"
            aria-pressed={!letter}
            onClick={() => patchParam("letter", null)}
          >
            {t("browse.jump.all")}
          </button>
        </li>
        {facets.letters.map((l) => (
          <li key={l.letter}>
            <button
              type="button"
              className="mkt-jumpbar__btn"
              aria-pressed={letter === l.letter}
              aria-label={t("browse.jump.letter", { letter: l.letter, n: n(l.videos) })}
              onClick={() => patchParam("letter", letter === l.letter ? null : l.letter)}
            >
              {l.letter}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );

  // A card's marks: the tick of a watched video, how far an unfinished one got
  // and the heart of a favorite.
  const marks = (v: Video) => {
    const frac = progressOf(v);
    return (
      <>
        {v.playback_state?.completed && (
          <span className="mkt-card__watched" role="img" aria-label={t("browse.watchedMark")}>
            ✓
          </span>
        )}
        {v.favorite && (
          <span className="mkt-card__fav" role="img" aria-label={t("browse.favoriteMark")}>
            ♥
          </span>
        )}
        {frac !== null && (
          <span
            className="mkt-card__progress"
            role="progressbar"
            aria-label={t("home.watched")}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(frac * 100)}
            aria-valuetext={t("home.watchedPct", { pct: n(Math.round(frac * 100)) })}
          >
            <span style={{ inlineSize: `${frac * 100}%` }} />
          </span>
        )}
      </>
    );
  };

  const cardBody = (v: Video) => (
    <>
      <span className="mkt-card__art">
        <VideoPoster videoId={v.id} />
        {marks(v)}
      </span>
      <div className="mkt-card__title" dir="auto">
        {v.title || v.filename}
      </div>
      <div className="mkt-card__meta">
        {typeof v.duration_sec === "number" && <span>{formatDuration(v.duration_sec)}</span>}
        {v.detected_language && <span> · {v.detected_language}</span>}
        {sort === "published_at" && v.published_at && <span> · {formatDate(v.published_at)}</span>}
        {typeof v.plays === "number" && <span> · {t("videosAll.plays", { n: n(v.plays) })}</span>}
      </div>
      <StateBadge state={v.state} />
    </>
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

      {jumpBar}
      {actions}
      {bulkBar}

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
              <Button variant="secondary" onClick={clearFilters}>
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
            <li
              key={v.id}
              className={`mkt-card${selecting && selected.includes(v.id) ? " mkt-card--selected" : ""}`}
            >
              {selecting ? (
                <label className="mkt-card__select">
                  <input
                    type="checkbox"
                    checked={selected.includes(v.id)}
                    onChange={() => toggleSelected(v.id)}
                    aria-label={t("browse.select.one", { name: v.title || v.filename })}
                  />
                  {cardBody(v)}
                </label>
              ) : (
                <Link to={`/videos/${v.id}`}>{cardBody(v)}</Link>
              )}
            </li>
          ))}
        </ul>
      )}

      {videos !== null && videos.length > 0 && view === "list" && (
        <table className="mkt-table" aria-label={listName}>
          <thead>
            <tr>
              {selecting && (
                <th>
                  <span className="mkt-visually-hidden">{t("browse.select")}</span>
                </th>
              )}
              <th>{t("library.col.title")}</th>
              <th>{t("library.col.duration")}</th>
              <th>{t("library.col.language")}</th>
              <th>{t("browse.col.watched")}</th>
              <th>{t("library.col.state")}</th>
            </tr>
          </thead>
          <tbody>
            {videos.map((v) => {
              const frac = progressOf(v);
              return (
                <tr key={v.id}>
                  {selecting && (
                    <td>
                      <input
                        type="checkbox"
                        checked={selected.includes(v.id)}
                        onChange={() => toggleSelected(v.id)}
                        aria-label={t("browse.select.one", { name: v.title || v.filename })}
                      />
                    </td>
                  )}
                  <td dir="auto">
                    <Link to={`/videos/${v.id}`}>{v.title || v.filename}</Link>
                    {v.favorite && (
                      <span
                        className="mkt-card__fav-inline"
                        role="img"
                        aria-label={t("browse.favoriteMark")}
                      >
                        {" "}
                        ♥
                      </span>
                    )}
                  </td>
                  <td>
                    {typeof v.duration_sec === "number" ? formatDuration(v.duration_sec) : "—"}
                  </td>
                  <td>{v.detected_language ?? "—"}</td>
                  <td>
                    {v.playback_state?.completed
                      ? t("browse.watchedMark")
                      : frac !== null
                        ? t("home.watchedPct", { pct: n(Math.round(frac * 100)) })
                        : "—"}
                  </td>
                  <td>{PROBLEM_TONE[v.state] ? <StateBadge state={v.state} /> : "—"}</td>
                </tr>
              );
            })}
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

      <AddToPlaylist
        open={playlistOpen}
        videoIds={selected}
        onClose={() => setPlaylistOpen(false)}
        onAdded={() => setSelected([])}
      />
      {curator && libraryId && (
        <AddToCollection
          open={collectionOpen}
          libraryId={libraryId}
          videoIds={selected}
          onClose={() => setCollectionOpen(false)}
          onAdded={() => setSelected([])}
        />
      )}
      {curator && (
        <Modal
          open={saveOpen}
          onClose={() => setSaveOpen(false)}
          title={t("collections.saveAsTitle")}
          footer={
            <>
              <Button variant="ghost" onClick={() => setSaveOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button
                disabled={saving || !saveName.trim()}
                loading={saving}
                onClick={() => void saveFilters()}
              >
                {t("common.save")}
              </Button>
            </>
          }
        >
          <p className="mkt-muted">{t("collections.saveAsDesc")}</p>
          <label className="mkt-field">
            <span className="mkt-field__label">{t("collections.name")}</span>
            <input
              type="text"
              dir="auto"
              value={saveName}
              maxLength={COLLECTION_NAME_MAX}
              onChange={(e) => setSaveName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void saveFilters();
              }}
            />
          </label>
        </Modal>
      )}
    </section>
  );
}
