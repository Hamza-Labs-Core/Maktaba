// Epic 45 — Home (`/`): rows across every library the user may see
// (GET /api/home): Continue Watching, On Deck (the next lesson of each series
// the user is in the middle of), Recently Added (grouped by series, story
// 45.18), Recently Watched, Favorites, the
// Live TV channels on now (WhatsOnNow, its own read), then one row per pinned
// library (its title opens the library). An empty row is not shown; a home
// with nothing at all says how to start (admins: Manage libraries).
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { fetchHome, type HomeResponse } from "../lib/browse";
import { TitleRow } from "../help/HelpLink";
import { WhatsOnNow } from "../components/WhatsOnNow";
import { AddedRow, VideoRow } from "../components/MediaRow";

export function Home() {
  const { t } = useI18n();
  const { user } = useAuth();
  const [home, setHome] = useState<HomeResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    fetchHome()
      .then((h) => {
        if (!cancelled) setHome(h);
      })
      .catch((e) => {
        if (!cancelled) setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
      });
    return () => {
      cancelled = true;
    };
  }, [reload, t]);

  const pinned = (home?.pinned ?? []).filter(
    (p) => p.continue_watching.length > 0 || p.recently_added.length > 0
  );
  const empty =
    !!home &&
    home.continue_watching.length === 0 &&
    home.on_deck.length === 0 &&
    home.favorites.length === 0 &&
    home.recently_added.length === 0 &&
    home.recently_watched.length === 0 &&
    pinned.length === 0;

  return (
    <section className="mkt-page mkt-home">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("home.title")}</h1>
        </TitleRow>
      </header>

      {err && (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={err}
          action={<Button onClick={() => setReload((x) => x + 1)}>{t("common.retry")}</Button>}
        />
      )}

      {!home && !err && (
        <div aria-busy="true" className="mkt-home__loading">
          <Skeleton variant="rect" height={140} />
          <Skeleton variant="rect" height={140} />
        </div>
      )}

      {home && (
        <>
          <VideoRow
            title={t("home.row.continueWatching")}
            videos={home.continue_watching}
            testId="row-continue"
          />
          <VideoRow
            title={t("home.row.onDeck")}
            videos={home.on_deck}
            meta={(v) => (v.series?.name ? <bdi>{v.series.name}</bdi> : null)}
            testId="row-ondeck"
          />
          <AddedRow
            title={t("home.row.recentlyAdded")}
            seeAllTo="/videos?sort=created_at"
            entries={home.recently_added_grouped}
            fallback={home.recently_added}
            testId="row-added"
          />
          <VideoRow
            title={t("home.row.recentlyWatched")}
            videos={home.recently_watched}
            testId="row-watched"
          />
          <VideoRow
            title={t("home.row.favorites")}
            seeAllTo="/videos?favorite=true"
            videos={home.favorites}
            testId="row-favorites"
          />
        </>
      )}

      <WhatsOnNow />

      {home &&
        pinned.map((p) => (
          <VideoRow
            key={p.library.id}
            title={<bdi>{p.library.name}</bdi>}
            titleTo={`/library/${encodeURIComponent(p.library.id)}`}
            // what you are in the middle of first, then what is new
            videos={dedupe([...p.continue_watching, ...p.recently_added])}
            testId={`row-pinned-${p.library.id}`}
          />
        ))}

      {empty && (
        <EmptyState
          kind="first_run"
          title={t("home.empty.title")}
          description={user?.is_admin ? t("home.empty.descAdmin") : t("home.empty.desc")}
          action={
            <span className="mkt-home__start">
              <Link className="mkt-btn mkt-btn--primary" to="/library">
                {t("home.empty.libraries")}
              </Link>
              {user?.is_admin && (
                <Link className="mkt-btn mkt-btn--ghost" to="/admin/libraries">
                  {t("home.empty.manage")}
                </Link>
              )}
            </span>
          }
        />
      )}
    </section>
  );
}

function dedupe<T extends { id: string }>(items: T[]): T[] {
  const seen = new Set<string>();
  return items.filter((i) => (seen.has(i.id) ? false : (seen.add(i.id), true)));
}
