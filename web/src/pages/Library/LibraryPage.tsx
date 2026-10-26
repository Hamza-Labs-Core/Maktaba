// Epic 45 — a library's page (`/library/:libraryId`), Plex's library section:
//
//   Overview (default): the hub's rows (GET /api/libraries/{id}/hub):
//     Continue Watching, Recently Added, Recently Watched, Most Watched,
//     Series (each opens /series/:id) and By year (a chip opens Browse
//     filtered to that year). Empty rows are not shown.
//   Browse (?tab=browse): the full grid (LibraryBrowser, embedded) with its
//     sort and filters in the URL.
//
// Pin / Unpin for everyone; admins also get Scan now and Manage. A library
// the user may not see answers 404, the same as one that does not exist.
import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { fetchHub, type LibraryHub } from "../../lib/browse";
import { TitleRow } from "../../help/HelpLink";
import { MediaRow, VideoRow } from "../../components/MediaRow";
import { LibraryBrowser, ScanNow } from "../LibraryBrowser";
import { PinButton } from "./Libraries";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

type Tab = "overview" | "browse";

export function LibraryPage() {
  const { libraryId = "" } = useParams();
  const { t, n } = useI18n();
  const { user } = useAuth();
  const [params, setParams] = useSearchParams();
  const tab: Tab = params.get("tab") === "browse" ? "browse" : "overview";
  const [hub, setHub] = useState<LibraryHub | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const valid = UUID.test(libraryId);

  useEffect(() => {
    let cancelled = false;
    setHub(null);
    setErr(null);
    setNotFound(false);
    // the server ignores a malformed id elsewhere; here it is simply not found
    if (!valid) {
      setNotFound(true);
      return;
    }
    fetchHub(libraryId)
      .then((h) => {
        if (!cancelled) setHub(h);
      })
      .catch((e) => {
        if (cancelled) return;
        if (e instanceof ApiError && e.status === 404) setNotFound(true);
        else setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
      });
    return () => {
      cancelled = true;
    };
  }, [libraryId, valid, reload, t]);

  function selectTab(next: string) {
    const p = new URLSearchParams(params);
    if (next === "browse") p.set("tab", "browse");
    else p.delete("tab");
    setParams(p, { replace: true });
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

  if (err) {
    return (
      <section className="mkt-page mkt-library-page">
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={err}
          action={<Button onClick={() => setReload((x) => x + 1)}>{t("common.retry")}</Button>}
        />
      </section>
    );
  }

  if (!hub) {
    return (
      <section className="mkt-page mkt-library-page" aria-busy="true">
        <Skeleton variant="text" lines={1} />
        <Skeleton variant="rect" height={140} />
      </section>
    );
  }

  const { rows } = hub;
  const name = hub.library.name;
  const browseYear = (y: number) =>
    `/library/${encodeURIComponent(libraryId)}?tab=browse&year=${y}`;
  const overviewEmpty =
    rows.continue_watching.length === 0 &&
    rows.recently_added.length === 0 &&
    rows.recently_watched.length === 0 &&
    rows.most_watched.length === 0 &&
    rows.series.length === 0 &&
    rows.years.length === 0;

  const overview = (
    <div className="mkt-library-page__overview">
      <VideoRow
        title={t("home.row.continueWatching")}
        videos={rows.continue_watching}
        testId="row-continue"
      />
      <VideoRow
        title={t("home.row.recentlyAdded")}
        videos={rows.recently_added}
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
      <MediaRow title={t("libraryPage.row.series")} seeAllTo="/series" testId="row-series">
        {rows.series.map((s) => (
          <li key={s.id} className="mkt-row__item">
            <Link to={`/series/${encodeURIComponent(s.id)}`} className="mkt-series-tile">
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
      <header className="mkt-page__header">
        <TitleRow>
          <h1>
            <bdi>{name}</bdi>
          </h1>
        </TitleRow>
        <span className="mkt-muted">{t("libraries.videos", { n: n(hub.library.videos) })}</span>
        <div className="mkt-library-page__actions">
          <PinButton lib={{ id: libraryId, name }} />
          {user?.is_admin && (
            <>
              <ScanNow libraryId={libraryId} onDone={() => setReload((x) => x + 1)} />
              <Link className="mkt-btn mkt-btn--ghost" to="/admin/libraries">
                {t("libraryPage.manage")}
              </Link>
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
        ]}
      />
    </section>
  );
}
