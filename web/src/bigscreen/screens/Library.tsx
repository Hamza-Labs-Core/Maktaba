// Epic 58 story 58.08 — a library on the big screen, laid out as the Apple TV
// and Android TV apps (Epic 56 §6.4):
//
//   Library name                       [▶ Play]  [⤮ Shuffle]
//   Recommended   Library   Collections   Playlists   Categories
//   ───────────────────────────────────────────────────────────
//   the tab's content
//
// - Recommended: the library's hub rows (GET /api/libraries/{id}/hub), with the
//   collections an admin promoted to it.
// - Library: Show [Videos | Series], Sort, Filter, a grid (GET /api/videos, or
//   ?group=series) and the letter rail at its end side (GET /api/videos/facets).
// - Collections: the library's collections with a badge per kind (YouTube,
//   Plex, Smart); Playlists: the user's lists with videos here; Categories:
//   speakers, topics, languages and years, each opening the filtered grid.
//
// Remote (Epic 56 §6.5): opening a library lands on its tab row; Left and Right
// move along it and the focused tab shows; Up reaches Play and Shuffle; Down
// enters the tab, where the tab left its focus (each tab keeps its focused item
// and scroll). Back steps out: to the top of the tab, then to the tab row, then
// out of the library. Play and Shuffle play the Library tab's current view on
// the server queue, its sort and filters included.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { fetchHub, type LibraryHub, type VideoCard } from "../../lib/browse";
import { fetchFacets, fetchPlaylists, type Facets, type PlaylistSummary } from "../../lib/me";
import { fetchLibraryCollections, type LibraryCollection } from "../../lib/libraryTabs";
import { useFeature } from "../../lib/capabilities";
import { useAuth } from "../../lib/auth";
import { errorText } from "../../lib/errorText";
import { seriesPictureUrl } from "../../pages/Library/SeriesBrowser";
import { useFocusApi } from "../focus/FocusProvider";
import { ACTION_EVENT, type ActionDetail } from "../input/actions";
import { bigPath, useBigNav } from "../nav";
import { usePlay } from "../play";
import {
  CardTile,
  Collage,
  Empty,
  Failed,
  Grid,
  Loading,
  Ltr,
  Panel,
  Picture,
  Row,
  VideoTile,
  posterOf,
} from "../ui";
import {
  SERIES_SORTS,
  SERIES_SORT_IDS,
  SORTS,
  SORT_IDS,
  TABS,
  WATCHED,
  filterCount,
  headerPlays,
  queueFilters,
  railLetters,
  readView,
  videosQuery,
  writeView,
  type LibraryTab,
  type LibraryView,
} from "../libraryView";

/** How many cards one page of the grid reads. */
export const GRID_PAGE = 48;
/** How long a library's answers are reused when coming back to it. */
const KEEP_MS = 60_000;

const TAB_KEYS: Record<LibraryTab, string> = {
  recommended: "library.tab.recommended",
  library: "library.tab.library",
  collections: "library.tab.collections",
  playlists: "library.tab.playlists",
  categories: "library.tab.categories",
};

// ---- what each tab keeps while the user is elsewhere ------------------------

interface TabMemory {
  /** the data-bs-focus id the tab last had focused */
  focus?: string;
  /** the screen's scroll on that tab */
  scroll?: number;
}
const tabMemory = new Map<string, TabMemory>();
const answers = new Map<string, { at: number; value: unknown }>();

// whose answers they are: another sign-in (a household member, a child's
// profile) never sees the last one's watch state or playlists
let owner: string | null = null;

/** Test seam: forgets every tab's focus, scroll and kept answers. */
export function resetLibraryMemory() {
  tabMemory.clear();
  answers.clear();
  owner = null;
}

/** Keeps the answers only for the user they were read for. */
function keepFor(userId: string | null) {
  if (owner === userId) return;
  tabMemory.clear();
  answers.clear();
  owner = userId;
}

async function kept<T>(key: string, load: () => Promise<T>): Promise<T> {
  const hit = answers.get(key);
  if (hit && Date.now() - hit.at < KEEP_MS) return hit.value as T;
  const value = await load();
  answers.set(key, { at: Date.now(), value });
  return value;
}

function keptNow<T>(key: string): T | undefined {
  const hit = answers.get(key);
  return hit && Date.now() - hit.at < KEEP_MS ? (hit.value as T) : undefined;
}

function scroller(): HTMLElement | null {
  return document.querySelector<HTMLElement>("[data-bs-scroll]");
}

// ---- the screen -------------------------------------------------------------

export function BigLibrary() {
  const { id = "" } = useParams();
  const { user } = useAuth();
  // before any kept answer is read below
  keepFor(user?.id ?? null);
  const [params] = useSearchParams();
  const view = useMemo(() => readView(params), [params]);
  const location = useLocation();
  const navigate = useNavigate();
  const { t, n } = useI18n();
  const focus = useFocusApi();
  const play = usePlay();
  const [hub, setHub] = useState<LibraryHub | null>(() => keptNow(`hub:${id}`) ?? null);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [reload, setReload] = useState(0);
  const panelRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    kept(`hub:${id}`, () => fetchHub(id))
      .then((h) => !cancelled && setHub(h))
      .catch(
        (e) =>
          !cancelled &&
          setErr(
            e instanceof ApiError
              ? { message: errorText(e, t), status: e.status }
              : { message: errorText(e, t) }
          )
      );
    return () => {
      cancelled = true;
    };
  }, [id, reload, t]);

  /** Shows another view of the library (the URL holds it); Back is not one step per change. */
  const show = useCallback(
    (next: LibraryView) => {
      const qs = writeView(next);
      navigate(`${location.pathname}${qs ? `?${qs}` : ""}`, {
        replace: true,
        state: location.state,
      });
    },
    [navigate, location.pathname, location.state]
  );

  // each tab's focus and scroll: kept as the user moves, given back on return
  const memKey = (tab: LibraryTab) => `${id}:${tab}`;
  const tabRef = useRef(view.tab);
  tabRef.current = view.tab;
  useEffect(() => {
    const panel = panelRef.current;
    if (!panel) return;
    const onFocusIn = (e: FocusEvent) => {
      const own = (e.target as HTMLElement | null)?.getAttribute?.("data-bs-focus");
      if (!own) return;
      const k = memKey(tabRef.current);
      tabMemory.set(k, { ...tabMemory.get(k), focus: own });
    };
    panel.addEventListener("focusin", onFocusIn);
    return () => panel.removeEventListener("focusin", onFocusIn);
    // the panel is there once the hub is
  }, [hub, id]);

  const switchTab = (tab: LibraryTab) => {
    if (tab === view.tab) return;
    const s = scroller();
    const k = memKey(view.tab);
    tabMemory.set(k, { ...tabMemory.get(k), scroll: s?.scrollTop ?? 0 });
    show({ ...view, tab });
  };
  useLayoutEffect(() => {
    const s = scroller();
    if (s) s.scrollTop = tabMemory.get(memKey(view.tab))?.scroll ?? 0;
  }, [view.tab]);

  // Down from the tab row enters the tab where it was left; Back steps out
  const state = useRef({ view, focus });
  state.current = { view, focus };
  useEffect(() => {
    const onAction = (e: Event) => {
      const { action } = (e as CustomEvent<ActionDetail>).detail;
      const { view: v, focus: f } = state.current;
      const active = document.activeElement as HTMLElement | null;
      const panel = panelRef.current;
      if (!panel || !active) return;
      const onTab = !!active.closest("[data-bs-libtabs]");
      if (action === "down" && onTab) {
        const want = tabMemory.get(memKey(v.tab))?.focus;
        const el = want
          ? panel.querySelector<HTMLElement>(`[data-bs-focus="${cssId(want)}"]`)
          : null;
        if (el) {
          e.preventDefault();
          f.focusEl(el);
        }
        return;
      }
      if (action !== "back" || !panel.contains(active)) return;
      e.preventDefault();
      const top = firstControl(panel);
      const s = scroller();
      if (top && (active !== top || (s?.scrollTop ?? 0) > 0)) {
        // first: the top of the tab, its first row
        if (s) s.scrollTop = 0;
        f.focusEl(top);
      } else {
        // then: the tab row
        const tab = document.querySelector<HTMLElement>(`[data-bs-focus="ltab:${v.tab}"]`);
        if (tab) f.focusEl(tab);
      }
    };
    window.addEventListener(ACTION_EVENT, onAction);
    return () => window.removeEventListener(ACTION_EVENT, onAction);
  }, [id]);

  if (err)
    return (
      <Failed
        message={err.status === 404 ? t("library.notFound") : err.message}
        onRetry={err.status === 404 ? undefined : () => setReload((x) => x + 1)}
      />
    );
  if (!hub) return <Loading />;

  const total = hub.library.videos;
  const queue = (shuffle: boolean) =>
    void play({ kind: "library", id, filters: queueFilters(view) }, { shuffle });

  return (
    <section className="bs-screen bs-library" data-testid="big-library">
      <header className="bs-library__head">
        <div className="bs-library__name">
          <h1 className="bs-screen__title">
            <bdi>{hub.library.name}</bdi>
          </h1>
          <p className="bs-screen__sub">{t("big.videoCount", { n: n(total) })}</p>
        </div>
        {headerPlays(view.tab) && (
          <div className="bs-library__plays" data-bs-group="library-actions">
            <button
              type="button"
              className="bs-btn bs-btn--primary"
              data-bs-focus="library:play"
              disabled={total === 0}
              onClick={() => queue(false)}
            >
              ▶ {t("big.play")}
            </button>
            <button
              type="button"
              className="bs-btn"
              data-bs-focus="library:shuffle"
              disabled={total < 2}
              onClick={() => queue(true)}
            >
              ⤮ {t("big.shuffle")}
            </button>
          </div>
        )}
      </header>
      <nav
        className="bs-libtabs"
        role="tablist"
        aria-label={t("libraryPage.tabs")}
        data-bs-group="library-tabs"
        data-bs-libtabs
      >
        {TABS.map((tab) => (
          <button
            key={tab}
            type="button"
            role="tab"
            id={`bs-ltab-${tab}`}
            aria-selected={tab === view.tab}
            aria-controls="bs-ltab-panel"
            className="bs-libtab"
            data-bs-focus={`ltab:${tab}`}
            data-bs-autofocus={tab === view.tab || undefined}
            onFocus={() => switchTab(tab)}
            onClick={() => switchTab(tab)}
          >
            {t(TAB_KEYS[tab])}
          </button>
        ))}
      </nav>
      <div
        ref={panelRef}
        id="bs-ltab-panel"
        role="tabpanel"
        aria-labelledby={`bs-ltab-${view.tab}`}
        className="bs-libpanel"
        data-testid={`big-library-${view.tab}`}
      >
        {view.tab === "recommended" && (
          <RecommendedTab hub={hub} onBrowse={() => switchTab("library")} />
        )}
        {view.tab === "library" && <LibraryTabView libraryId={id} view={view} show={show} />}
        {view.tab === "collections" && <CollectionsTab libraryId={id} />}
        {view.tab === "playlists" && <PlaylistsTab libraryId={id} />}
        {view.tab === "categories" && (
          <CategoriesTab
            libraryId={id}
            onPick={(k, value) =>
              show({ ...view, tab: "library", show: "videos", filters: { [k]: value } })
            }
          />
        )}
      </div>
    </section>
  );
}

/** The tab's first control in reading order (the top of its first row). */
function firstControl(panel: HTMLElement): HTMLElement | null {
  return panel.querySelector<HTMLElement>("[data-bs-focus]:not([disabled])");
}

function cssId(s: string): string {
  return typeof CSS !== "undefined" && CSS.escape ? CSS.escape(s) : s.replace(/["\\]/g, "\\$&");
}

// ---- Recommended ------------------------------------------------------------

function RecommendedTab({ hub, onBrowse }: { hub: LibraryHub; onBrowse: () => void }) {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const r = hub.rows;
  const open = (v: VideoCard) => go(bigPath.video(v.id));
  const lesson = (v: VideoCard) =>
    go(
      v.series
        ? `${bigPath.video(v.id)}?series=${encodeURIComponent(v.series.id)}`
        : bigPath.video(v.id)
    );
  const empty =
    r.on_deck.length +
      r.continue_watching.length +
      r.recently_added.length +
      r.recently_watched.length +
      r.most_watched.length +
      r.series.length +
      hub.collection_rows.length ===
    0;
  if (empty)
    return (
      <Empty>
        <p>{t("library.empty.recommended")}</p>
        <button type="button" className="bs-btn" data-bs-focus="rec:browse" onClick={onBrowse}>
          {t("library.tab.library")}
        </button>
      </Empty>
    );
  return (
    <>
      <Row id="rec-ondeck" title={t("library.row.onDeck")} count={r.on_deck.length}>
        {r.on_deck.map((v) => (
          <VideoTile
            key={v.id}
            video={v}
            focusId={`lo:${v.id}`}
            meta={v.series ? <bdi>{v.series.name}</bdi> : undefined}
            onSelect={() => lesson(v)}
          />
        ))}
      </Row>
      <Row id="rec-continue" title={t("library.row.continue")} count={r.continue_watching.length}>
        {r.continue_watching.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`lc:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="rec-added" title={t("library.row.recentlyAdded")} count={r.recently_added.length}>
        {r.recently_added.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`la:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      {hub.collection_rows.map((row) => (
        <Row
          key={row.collection.id}
          id={`rec-col:${row.collection.id}`}
          title={<bdi>{row.collection.name}</bdi>}
          count={row.items.length}
        >
          {row.items.map((v) => (
            <VideoTile
              key={v.id}
              video={v}
              focusId={`lp:${row.collection.id}:${v.id}`}
              onSelect={() => open(v)}
            />
          ))}
        </Row>
      ))}
      <Row
        id="rec-watched"
        title={t("library.row.recentlyWatched")}
        count={r.recently_watched.length}
      >
        {r.recently_watched.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`lw:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="rec-most" title={t("big.row.mostWatched")} count={r.most_watched.length}>
        {r.most_watched.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`lm:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="rec-series" title={t("big.tab.series")} count={r.series.length}>
        {r.series.map((s) => (
          <SeriesCard
            key={s.id}
            id={s.id}
            name={s.name}
            posterVideoId={s.poster_video_id ?? null}
            meta={t("big.lessonCount", { n: n(s.episodes) })}
            focusId={`ls:${s.id}`}
          />
        ))}
      </Row>
    </>
  );
}

/** A series' card: opens its page; Play/Pause continues it. */
export function SeriesCard({
  id,
  name,
  posterVideoId,
  posterPath,
  meta,
  focusId,
}: {
  id: string;
  name: string | null;
  posterVideoId?: string | null;
  posterPath?: string | null;
  meta?: React.ReactNode;
  focusId: string;
}) {
  const { t } = useI18n();
  const { go } = useBigNav();
  const play = usePlay();
  const title = name || t("big.series.untitled");
  return (
    <CardTile
      focusId={focusId}
      title={title}
      meta={meta}
      art={
        <Picture
          src={seriesPictureUrl({ poster_path: posterPath, poster_video_id: posterVideoId })}
        />
      }
      actions={{
        kind: "series",
        title,
        play: () => void play({ kind: "series", id }, { series: id }),
      }}
      onSelect={() => go(bigPath.series(id))}
    />
  );
}

// ---- Library ----------------------------------------------------------------

interface GridItem {
  kind: "video" | "series";
  id: string;
  name?: string;
  poster_video_id?: string;
  episode_count?: number;
  title?: string | null;
  display_title?: string | null;
  filename?: string;
  duration_sec?: number | null;
  playback_state?: { position_sec: number; completed: boolean } | null;
}
interface GridPage {
  items: GridItem[];
  next: string | null;
}

function readPage(r: unknown, grouped: boolean): GridPage {
  const o = (r && typeof r === "object" ? r : {}) as { items?: unknown; next?: unknown };
  const items = (Array.isArray(o.items) ? o.items : [])
    .filter((x): x is GridItem => !!x && typeof (x as GridItem).id === "string")
    .map((x) => ({ ...x, kind: grouped && x.kind === "series" ? "series" : "video" }) as GridItem);
  return { items, next: typeof o.next === "string" && o.next ? o.next : null };
}

function LibraryTabView({
  libraryId,
  view,
  show,
}: {
  libraryId: string;
  view: LibraryView;
  show: (v: LibraryView) => void;
}) {
  const { t, n, locale } = useI18n();
  const { go } = useBigNav();
  const query = videosQuery(libraryId, view, GRID_PAGE).toString();
  const grouped = view.show === "series";
  const [page, setPage] = useState<GridPage | null>(() => keptNow(`grid:${query}`) ?? null);
  const [err, setErr] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [facets, setFacets] = useState<Facets | null>(() => keptNow(`facets:${libraryId}`) ?? null);
  const [panel, setPanel] = useState<"sort" | "filter" | null>(null);
  const [reload, setReload] = useState(0);
  const gen = useRef(0);

  useEffect(() => {
    let cancelled = false;
    const g = ++gen.current;
    setErr(null);
    setPage(keptNow(`grid:${query}`) ?? null);
    kept(`grid:${query}`, () =>
      api.get<unknown>(`/api/videos?${query}`).then((r) => readPage(r, grouped))
    )
      .then((p) => !cancelled && g === gen.current && setPage(p))
      .catch((e) => !cancelled && setErr(errorText(e, t)));
    return () => {
      cancelled = true;
    };
  }, [query, grouped, reload, t]);

  useEffect(() => {
    let cancelled = false;
    kept(`facets:${libraryId}`, () => fetchFacets(libraryId))
      .then((f) => !cancelled && setFacets(f))
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [libraryId]);

  const more = useCallback(() => {
    if (!page?.next || loadingMore) return;
    const g = gen.current;
    setLoadingMore(true);
    const q = new URLSearchParams(query);
    q.set("cursor", page.next);
    api
      .get<unknown>(`/api/videos?${q}`)
      .then((r) => {
        if (g !== gen.current) return;
        const p = readPage(r, grouped);
        setPage((cur) => {
          const merged = { items: [...(cur?.items ?? []), ...p.items], next: p.next };
          answers.set(`grid:${query}`, { at: Date.now(), value: merged });
          return merged;
        });
      })
      .catch(() => undefined)
      .finally(() => setLoadingMore(false));
  }, [page, loadingMore, query, grouped]);

  const letters = railLetters(
    (facets?.letters ?? []).map((l) => l.letter),
    locale === "ar" ? "ar" : "en"
  );
  const sorts = grouped ? SERIES_SORTS : SORTS;
  const sortKey =
    (sorts as Record<string, { key: string }>)[view.sort]?.key ?? "library.sort.newest";
  const count = filterCount(view);
  const items = page?.items ?? [];

  return (
    <>
      <div className="bs-libbar" data-bs-group="library-bar">
        <span className="bs-libbar__label">{t("library.show")}</span>
        {(["videos", "series"] as const).map((s) => (
          <button
            key={s}
            type="button"
            className="bs-chip"
            data-bs-focus={`show:${s}`}
            aria-pressed={view.show === s}
            onClick={() => show({ ...view, show: s, sort: "newest" })}
          >
            {t(s === "videos" ? "library.show.videos" : "library.show.series")}
          </button>
        ))}
        <button
          type="button"
          className="bs-chip"
          data-bs-focus="lib:sort"
          onClick={() => setPanel("sort")}
        >
          {t("library.sort")}: {t(sortKey)}
        </button>
        <button
          type="button"
          className="bs-chip"
          data-bs-focus="lib:filter"
          aria-pressed={count > 0}
          onClick={() => setPanel("filter")}
        >
          {count > 0 ? t("library.filter.count", { n: n(count) }) : t("library.filter")}
        </button>
      </div>
      <div className="bs-libgrid">
        <div className="bs-libgrid__cards">
          {err ? (
            <Failed message={err} onRetry={() => setReload((x) => x + 1)} />
          ) : !page ? (
            <Loading />
          ) : items.length === 0 ? (
            <Empty>
              <p>
                {t(
                  count > 0 || view.filters.letter ? "library.empty.filtered" : "big.library.empty"
                )}
              </p>
            </Empty>
          ) : (
            <div
              onFocus={(e) => {
                // the next page comes as focus nears the end of the grid
                const cell = (e.target as HTMLElement).closest?.("[data-bs-index]");
                const i = Number(cell?.getAttribute("data-bs-index") ?? -1);
                if (i >= 0 && i >= items.length - 12) more();
              }}
            >
              <Grid id={`lib:${view.show}`}>
                {items.map((it, i) =>
                  it.kind === "series" ? (
                    <IndexedSeries key={`s:${it.id}`} item={it} index={i} />
                  ) : (
                    <IndexedVideo
                      key={it.id}
                      item={it}
                      index={i}
                      onSelect={() => go(bigPath.video(it.id))}
                    />
                  )
                )}
              </Grid>
              {page.next && (
                <div className="bs-more" data-bs-group="lib-more">
                  <button
                    type="button"
                    className="bs-btn"
                    data-bs-focus="lib:more"
                    disabled={loadingMore}
                    onClick={more}
                  >
                    {loadingMore ? t("common.loading") : t("common.loadMore")}
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
        {letters.length > 0 && (
          <nav
            className="bs-rail"
            aria-label={t("big.letters")}
            data-bs-group="letters"
            data-testid="big-letter-rail"
          >
            <button
              type="button"
              className="bs-rail__letter"
              data-bs-focus="letter:all"
              aria-pressed={!view.filters.letter}
              aria-label={t("big.letters.all")}
              onClick={() => show({ ...view, filters: { ...view.filters, letter: undefined } })}
            >
              •
            </button>
            {letters.map((l) => (
              <button
                key={l}
                type="button"
                className="bs-rail__letter"
                data-bs-focus={`letter:${l}`}
                aria-pressed={view.filters.letter === l}
                onClick={() =>
                  show({
                    ...view,
                    filters: { ...view.filters, letter: view.filters.letter === l ? undefined : l },
                  })
                }
              >
                {l}
              </button>
            ))}
          </nav>
        )}
      </div>
      {panel === "sort" && (
        <Panel title={t("library.sort")} onClose={() => setPanel(null)} testId="big-sort">
          {(grouped ? SERIES_SORT_IDS : SORT_IDS).map((s) => (
            <button
              key={s}
              type="button"
              className="bs-option"
              data-bs-focus={`sort:${s}`}
              data-bs-autofocus={view.sort === s || undefined}
              aria-pressed={view.sort === s}
              onClick={() => {
                setPanel(null);
                show({ ...view, sort: s });
              }}
            >
              {t((sorts as Record<string, { key: string }>)[s]!.key)}
            </button>
          ))}
        </Panel>
      )}
      {panel === "filter" && (
        <FilterPanel
          view={view}
          facets={facets}
          onChange={(filters) => show({ ...view, filters })}
          onClose={() => setPanel(null)}
        />
      )}
    </>
  );
}

function IndexedVideo({
  item,
  index,
  onSelect,
}: {
  item: GridItem;
  index: number;
  onSelect: () => void;
}) {
  const card: VideoCard = {
    id: item.id,
    title: item.display_title || item.title,
    filename: item.filename,
    duration_sec: item.duration_sec,
    position_sec: item.playback_state?.position_sec ?? null,
    completed: item.playback_state?.completed ?? null,
  };
  return (
    <span className="bs-cell" data-bs-index={index}>
      <VideoTile video={card} focusId={`lv:${item.id}`} onSelect={onSelect} />
    </span>
  );
}

function IndexedSeries({ item, index }: { item: GridItem; index: number }) {
  const { t, n } = useI18n();
  return (
    <span className="bs-cell" data-bs-index={index}>
      <SeriesCard
        id={item.id}
        name={item.name ?? null}
        posterVideoId={item.poster_video_id ?? null}
        meta={t("big.lessonCount", { n: n(item.episode_count ?? 0) })}
        focusId={`lsr:${item.id}`}
      />
    </span>
  );
}

function FilterPanel({
  view,
  facets,
  onChange,
  onClose,
}: {
  view: LibraryView;
  facets: Facets | null;
  onChange: (filters: LibraryView["filters"]) => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const f = view.filters;
  const toggle = (k: keyof LibraryView["filters"], v: string) =>
    onChange({ ...f, [k]: f[k] === v ? undefined : v });
  const option = (k: keyof LibraryView["filters"], v: string, label: React.ReactNode) => (
    <button
      key={`${k}:${v}`}
      type="button"
      className="bs-option"
      data-bs-focus={`filter:${k}:${v}`}
      aria-pressed={f[k] === v}
      onClick={() => toggle(k, v)}
    >
      {label}
    </button>
  );
  const watchedKey: Record<(typeof WATCHED)[number], string> = {
    unwatched: "library.filter.unwatched",
    in_progress: "library.filter.inProgress",
    watched: "library.filter.watched",
  };
  return (
    <Panel title={t("library.filter")} onClose={onClose} testId="big-filter">
      <button
        type="button"
        className="bs-option"
        data-bs-focus="filter:clear"
        disabled={filterCount(view) === 0}
        onClick={() => onChange({ letter: f.letter })}
      >
        {t("library.filter.clear")}
      </button>
      <h3 className="bs-panel__heading">{t("library.filter.watched")}</h3>
      {WATCHED.map((w) => option("watched", w, t(watchedKey[w])))}
      {(facets?.languages.length ?? 0) > 0 && (
        <h3 className="bs-panel__heading">{t("library.filter.language")}</h3>
      )}
      {facets?.languages.map((l) => option("language", l.code, <Ltr>{l.code.toUpperCase()}</Ltr>))}
      {(facets?.speakers.length ?? 0) > 0 && (
        <h3 className="bs-panel__heading">{t("library.filter.speaker")}</h3>
      )}
      {facets?.speakers.slice(0, 30).map((s) => option("speaker", s.name, <bdi>{s.name}</bdi>))}
      {(facets?.tags.length ?? 0) > 0 && (
        <h3 className="bs-panel__heading">{t("library.filter.topic")}</h3>
      )}
      {facets?.tags.slice(0, 30).map((g) => option("tag", g.id, <bdi>{g.name}</bdi>))}
    </Panel>
  );
}

// ---- Collections ------------------------------------------------------------

/** The badge of a collection's kind (YouTube, Plex, Smart), or none. */
export function collectionBadge(
  c: Pick<LibraryCollection, "kind" | "source_kind">,
  t: (k: string) => string
): string | null {
  if (c.source_kind === "youtube_playlist") return t("collections.badge.youtubePlaylist");
  if (c.source_kind === "youtube_channel") return t("collections.badge.youtubeChannel");
  if (c.source_kind === "plex" || c.kind === "plex") return t("collections.badge.plex");
  if (c.kind === "youtube") return t("collections.badge.youtubePlaylist");
  if (c.kind === "smart") return t("collections.badge.smart");
  return null;
}

/** A collection's queue source: an automatic group is topic:<gid> or clip:<gid>. */
export function collectionSource(c: Pick<LibraryCollection, "kind" | "id">): string {
  return c.kind === "topic" || c.kind === "clip" ? `${c.kind}:${c.id}` : c.id;
}

export function collectionPath(libraryId: string, c: Pick<LibraryCollection, "kind" | "id">) {
  return c.kind === "topic" || c.kind === "clip"
    ? bigPath.autoCollection(libraryId, c.kind, c.id)
    : bigPath.collection(c.id);
}

function CollectionsTab({ libraryId }: { libraryId: string }) {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const play = usePlay();
  const youtube = useFeature("youtube");
  const plex = useFeature("plex");
  const [items, setItems] = useState<LibraryCollection[] | null>(
    () => keptNow(`collections:${libraryId}`) ?? null
  );
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let cancelled = false;
    setErr(null);
    kept(`collections:${libraryId}`, async () => {
      // every page: a library has tens of collections, not thousands
      const out: LibraryCollection[] = [];
      let offset = 0;
      for (let i = 0; i < 20; i++) {
        const p = await fetchLibraryCollections(libraryId, "all", offset);
        out.push(...p.items);
        if (p.next === null) break;
        offset = p.next;
      }
      return out;
    })
      .then((x) => !cancelled && setItems(x))
      .catch((e) => !cancelled && setErr(errorText(e, t)));
    return () => {
      cancelled = true;
    };
  }, [libraryId, reload, t]);
  if (err) return <Failed message={err} onRetry={() => setReload((x) => x + 1)} />;
  if (!items) return <Loading />;
  // a source switched off on this home (story 51.11): its collections are not shown
  const shown = items.filter(
    (c) =>
      !(youtube === "off" && (c.kind === "youtube" || c.source_kind?.startsWith("youtube"))) &&
      !(plex === "off" && (c.kind === "plex" || c.source_kind === "plex"))
  );
  if (shown.length === 0) return <Empty>{t("collections.empty")}</Empty>;
  return (
    <Grid id="collections">
      {shown.map((c) => (
        <CardTile
          key={`${c.kind}:${c.id}`}
          focusId={`col:${c.kind}:${c.id}`}
          title={c.name}
          meta={c.videos !== null ? t("collections.count", { n: n(c.videos) }) : undefined}
          badge={collectionBadge(c, t)}
          art={<Collage videoIds={c.poster_video_ids} />}
          actions={{
            kind: "collection",
            title: c.name,
            play: () => void play({ kind: "collection", id: collectionSource(c) }),
          }}
          onSelect={() => go(collectionPath(libraryId, c))}
        />
      ))}
    </Grid>
  );
}

// ---- Playlists --------------------------------------------------------------

function PlaylistsTab({ libraryId }: { libraryId: string }) {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const play = usePlay();
  const [items, setItems] = useState<PlaylistSummary[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let cancelled = false;
    setErr(null);
    fetchPlaylists(libraryId)
      .then((x) => !cancelled && setItems(x))
      .catch((e) => !cancelled && setErr(errorText(e, t)));
    return () => {
      cancelled = true;
    };
  }, [libraryId, reload, t]);
  if (err) return <Failed message={err} onRetry={() => setReload((x) => x + 1)} />;
  if (!items) return <Loading />;
  if (items.length === 0) return <Empty>{t("playlists.empty.library")}</Empty>;
  return (
    <Grid id="playlists">
      {items.map((p) => (
        <CardTile
          key={p.id}
          focusId={`pl:${p.id}`}
          title={p.name}
          meta={t("playlists.count", { n: n(p.videos) })}
          badge={p.kind === "smart" ? t("collections.badge.smart") : null}
          art={<Collage videoIds={p.poster_video_ids} />}
          actions={{
            kind: "playlist",
            title: p.name,
            play: () => void play({ kind: "playlist", id: p.id }),
          }}
          onSelect={() => go(bigPath.playlist(p.id))}
        />
      ))}
    </Grid>
  );
}

// ---- Categories -------------------------------------------------------------

interface Category {
  value: string;
  label: string;
  count: number;
  poster_video_id?: string;
}
type CategoryKind = "speakers" | "topics" | "languages" | "years";
const CATEGORY_FILTER: Record<CategoryKind, "speaker" | "tag" | "language" | "year"> = {
  speakers: "speaker",
  topics: "tag",
  languages: "language",
  years: "year",
};

function CategoriesTab({
  libraryId,
  onPick,
}: {
  libraryId: string;
  onPick: (k: "speaker" | "tag" | "language" | "year", value: string) => void;
}) {
  const { t, n } = useI18n();
  const [doc, setDoc] = useState<Record<CategoryKind, Category[]> | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let cancelled = false;
    setErr(null);
    kept(`categories:${libraryId}`, () =>
      api.get<Partial<Record<CategoryKind, Category[]>>>(
        `/api/libraries/${encodeURIComponent(libraryId)}/categories`
      )
    )
      .then((r) => {
        if (cancelled) return;
        const list = (k: CategoryKind) =>
          (Array.isArray(r?.[k]) ? r[k]! : []).filter((c) => c && typeof c.value === "string");
        setDoc({
          speakers: list("speakers"),
          topics: list("topics"),
          languages: list("languages"),
          years: list("years"),
        });
      })
      .catch((e) => !cancelled && setErr(errorText(e, t)));
    return () => {
      cancelled = true;
    };
  }, [libraryId, reload, t]);
  if (err) return <Failed message={err} onRetry={() => setReload((x) => x + 1)} />;
  if (!doc) return <Loading />;
  const kinds: CategoryKind[] = ["speakers", "topics", "languages", "years"];
  if (kinds.every((k) => doc[k].length === 0))
    return <Empty>{t("library.empty.categories")}</Empty>;
  return (
    <>
      {kinds.map((k) => (
        <Row key={k} id={`cat-${k}`} title={t(`library.categories.${k}`)} count={doc[k].length}>
          {doc[k].map((c) => (
            <CardTile
              key={c.value}
              focusId={`cat:${k}:${c.value}`}
              title={c.label}
              meta={t("big.videoCount", { n: n(c.count) })}
              art={<Picture src={c.poster_video_id ? posterOf(c.poster_video_id) : null} />}
              actions={null}
              onSelect={() => onPick(CATEGORY_FILTER[k], c.value)}
            />
          ))}
        </Row>
      ))}
    </>
  );
}
