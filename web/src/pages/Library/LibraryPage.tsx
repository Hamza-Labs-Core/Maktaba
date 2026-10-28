// Epic 45 — a library's page (`/library/:libraryId`), Plex's library section:
//
//   Overview (default): the hub's rows (GET /api/libraries/{id}/hub):
//     Continue Watching, Recently Added, Recently Watched, Most Watched,
//     Series (each opens /series/:id) and By year (a chip opens Browse
//     filtered to that year). Empty rows are not shown.
//     A Collections row (story 45.24): the library's collections, automatic
//     and curated (GET /api/libraries/{id}/collections), each opening its
//     page; its title and See all open the Collections tab, the Series row's
//     the Series tab.
//   Browse (?tab=browse): the full grid (LibraryBrowser, embedded) with its
//     sort and filters in the URL.
//   Series (?tab=series) and Collections (?tab=collections): LibraryTabs.
//
// Each tab is its own address (Back goes to the tab before); the old
// /library/:id/collections page redirects to the Collections tab.
//
// A pin icon by the title for everyone (pin to the sidebar); admins also get
// Scan now and Manage, at the header's end. A library
// the user may not see answers 404, the same as one that does not exist.
import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { fetchHub, type LibraryHub } from "../../lib/browse";
import { TitleRow } from "../../help/HelpLink";
import { AddedRow, MediaRow, VideoRow } from "../../components/MediaRow";
import { LibraryBrowser, ScanNow } from "../LibraryBrowser";
import { PinToggle } from "../../components/PinToggle";
import {
  fetchLibraryCollections,
  libraryTabPath,
  type CollectionsPage,
} from "../../lib/libraryTabs";
import { CollectionsTab, LibraryCollectionCard, SeriesTab, collectionsKey } from "./LibraryTabs";
import { SERVER_PAGES } from "../../lib/edition";
import { LoadError } from "../../components/LoadError";
import { LibraryPagePlaceholder, Loading } from "../../components/Placeholders";
import { usePageData } from "../../lib/usePageData";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const TABS = ["overview", "browse", "series", "collections"] as const;
type Tab = (typeof TABS)[number];
/** Cards a row of the Overview shows. */
const ROW_CARDS = 20;

export function LibraryPage() {
  const { libraryId = "" } = useParams();
  // a page per library: going from one library to another starts afresh, never
  // showing the first one's rows (or pinning it) under the second's id
  return <LibraryPageOf key={libraryId} libraryId={libraryId} />;
}

function LibraryPageOf({ libraryId }: { libraryId: string }) {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const [params, setParams] = useSearchParams();
  const rawTab = params.get("tab") as Tab | null;
  const tab: Tab = rawTab && TABS.includes(rawTab) ? rawTab : "overview";
  const valid = UUID.test(libraryId);
  // the server ignores a malformed id elsewhere; here it is simply not found
  const page = usePageData<LibraryHub>(
    valid ? `hub:${libraryId}` : null,
    () => fetchHub(libraryId),
    [t]
  );
  // its own read (the Collections tab's first page, kept for it): a failure
  // only leaves the row out
  const cols = usePageData<CollectionsPage>(valid ? collectionsKey(libraryId, "all") : null, () =>
    fetchLibraryCollections(libraryId, "all")
  );
  const { data: hub, err: hubErr, loading } = page;
  const collections = (cols.data?.items ?? []).slice(0, ROW_CARDS);
  const notFound = !valid || (hubErr instanceof ApiError && hubErr.status === 404);
  const err = notFound ? null : hubErr;
  function reload() {
    page.reload();
    cols.reload();
  }

  // a tab is a step in the history: Back returns to the one before
  function selectTab(next: string) {
    const p = new URLSearchParams(params);
    if (next === "overview") p.delete("tab");
    else p.set("tab", next);
    setParams(p);
  }

  if (notFound) {
    return (
      <section className="mkt-page mkt-library-page">
        <ErrorState
          kind="not_found"
          title={t("libraryPage.notFound")}
          description={t("libraryPage.notFound.desc")}
          action={
            <Link className="mkt-btn mkt-btn--primary" to="/library">
              {t("libraryPage.backToLibraries")}
            </Link>
          }
        />
      </section>
    );
  }

  if (err != null) {
    return (
      <section className="mkt-page mkt-library-page">
        <LoadError error={err} onRetry={reload} />
      </section>
    );
  }

  if (!hub) {
    return (
      <section className="mkt-page mkt-library-page">
        <Loading loading={loading}>
          <LibraryPagePlaceholder />
        </Loading>
      </section>
    );
  }

  const { rows } = hub;
  const name = hub.library.name;
  const browseYear = (y: number) =>
    `/library/${encodeURIComponent(libraryId)}?tab=browse&year=${y}`;
  const overviewEmpty =
    rows.continue_watching.length === 0 &&
    rows.on_deck.length === 0 &&
    rows.recently_added.length === 0 &&
    rows.recently_watched.length === 0 &&
    rows.most_watched.length === 0 &&
    rows.series.length === 0 &&
    rows.years.length === 0 &&
    collections.length === 0;
  const collectionsPath = libraryTabPath(libraryId, "collections");
  const seriesPath = libraryTabPath(libraryId, "series");

  const overview = (
    <div className="mkt-library-page__overview">
      <VideoRow
        title={t("home.row.continueWatching")}
        videos={rows.continue_watching}
        testId="row-continue"
      />
      <VideoRow
        title={t("home.row.onDeck")}
        videos={rows.on_deck}
        meta={(v) => (v.series?.name ? <bdi>{v.series.name}</bdi> : null)}
        testId="row-ondeck"
      />
      <AddedRow
        title={t("home.row.recentlyAdded")}
        entries={rows.recently_added_grouped}
        fallback={rows.recently_added}
        seeAllTo={`/library/${encodeURIComponent(libraryId)}?tab=browse&sort=created_at`}
        testId="row-added"
      />
      <VideoRow
        title={t("home.row.recentlyWatched")}
        videos={rows.recently_watched}
        testId="row-watched"
      />
      <VideoRow
        title={t("libraryPage.row.mostWatched")}
        videos={rows.most_watched}
        seeAllTo={`/library/${encodeURIComponent(libraryId)}?tab=browse&sort=plays`}
        testId="row-most"
      />
      <MediaRow
        title={t("collections.title")}
        titleTo={collectionsPath}
        seeAllTo={collectionsPath}
        testId="row-collections"
      >
        {collections.map((c) => (
          <LibraryCollectionCard
            key={`${c.kind}:${c.id}`}
            libraryId={libraryId}
            c={c}
            className="mkt-row__item mkt-libcard"
          />
        ))}
      </MediaRow>
      <MediaRow
        title={t("libraryPage.row.series")}
        titleTo={seriesPath}
        seeAllTo={seriesPath}
        testId="row-series"
      >
        {rows.series.map((s) => (
          <li key={s.id} className="mkt-row__item">
            <Link to={`/series/${encodeURIComponent(s.id)}`} className="mkt-series-tile">
              <SeriesTilePicture videoId={s.poster_video_id} />
              <bdi className="mkt-series-tile__name">
                {s.name || t("libraryPage.seriesUnnamed")}
              </bdi>
              <span className="mkt-series-tile__meta">
                {t("libraryPage.episodes", { n: n(s.episodes ?? 0) })}
              </span>
            </Link>
          </li>
        ))}
      </MediaRow>
      {rows.years.length > 0 && (
        <section className="mkt-row" aria-labelledby="mkt-library-years">
          <header className="mkt-row__head">
            <h2 id="mkt-library-years" className="mkt-row__title">
              {t("libraryPage.row.byYear")}
            </h2>
          </header>
          <ul className="mkt-year-chips">
            {rows.years.map((y) => (
              <li key={y.year}>
                <Link
                  to={browseYear(y.year)}
                  className="mkt-year-chip"
                  aria-label={t("libraryPage.yearChip", {
                    year: n(y.year, { useGrouping: false }),
                    n: n(y.videos),
                  })}
                >
                  <span>{n(y.year, { useGrouping: false })}</span>
                  <span className="mkt-year-chip__count">{n(y.videos)}</span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
      {overviewEmpty && (
        <EmptyState
          kind="first_run"
          title={t("library.empty.title")}
          // admins have Scan now in the header
          description={t("library.empty.desc")}
        />
      )}
    </div>
  );

  return (
    <section className="mkt-page mkt-library-page">
      <header className="mkt-library-page__header">
        <div className="mkt-library-page__heading">
          <TitleRow>
            <h1>
              <bdi>{name}</bdi>
            </h1>
            <PinToggle lib={{ id: libraryId, name }} />
          </TitleRow>
          <span className="mkt-muted">{t("libraries.videos", { n: n(hub.library.videos) })}</span>
        </div>
        <div className="mkt-library-page__actions">
          {user?.is_admin && (
            <>
              {SERVER_PAGES && (
                <Link className="mkt-btn mkt-btn--ghost" to="/admin/libraries">
                  {t("libraryPage.manage")}
                </Link>
              )}
              <ScanNow libraryId={libraryId} onDone={reload} />
            </>
          )}
        </div>
      </header>
      <Tabs
        label={t("libraryPage.tabs")}
        value={tab}
        onChange={selectTab}
        items={[
          { id: "overview", label: t("libraryPage.tab.overview"), content: overview },
          {
            id: "browse",
            label: t("libraryPage.tab.browse"),
            content: (
              <LibraryBrowser
                libraryId={libraryId}
                embedded
                years={rows.years.map((y) => y.year)}
              />
            ),
          },
          {
            id: "series",
            label: t("libraryPage.tab.series"),
            content: <SeriesTab libraryId={libraryId} />,
          },
          {
            id: "collections",
            label: t("libraryPage.tab.collections"),
            content: <CollectionsTab libraryId={libraryId} />,
          },
        ]}
      />
    </section>
  );
}

/** A series tile's picture: its first episode's poster, whole (a vertical video with
 *  bars at its sides); nothing when it has none or it does not load. */
function SeriesTilePicture({ videoId }: { videoId?: string | null }) {
  const [broken, setBroken] = useState(false);
  if (!videoId || broken) return null;
  return (
    <img
      className="mkt-series-tile__picture"
      src={`/api/videos/${encodeURIComponent(videoId)}/poster`}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  );
}
