// Epic 45 — Libraries (`/library`): a card per library the user may see
// (GET /api/libraries/summary): a 2x2 mosaic of recent posters, the name, the
// number of videos and a pin icon in its corner (shown on hover or focus,
// always when pinned); "All videos" opens the grid over every
// library. The pinned ones lead the page as a compact strip of links in the
// user's order, with a "Reorder" link to the Pinned libraries page
// (`/library/pinned`), where the order is set; the pins themselves go through
// lib/pins (PUT /api/me/pinned-libraries), which the sidebar's Pinned group follows.
import { useState } from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { fetchLibrarySummary, type LibrarySummary } from "../../lib/browse";
import { usePins } from "../../lib/pins";
import { PinToggle } from "../../components/PinToggle";
import { TitleRow } from "../../help/HelpLink";
import { SERVER_PAGES } from "../../lib/edition";
import { LoadError } from "../../components/LoadError";
import { usePageData } from "../../lib/usePageData";
import { CardGridPlaceholder, Loading } from "../../components/Placeholders";

/** The 2x2 poster mosaic of a library card; a library with fewer than four
 *  posters shows its newest one whole instead of blank tiles. */
export function PosterMosaic({ ids }: { ids: string[] }) {
  if (ids.length === 0) {
    // nothing to show yet: a quiet placeholder, not four blank tiles
    return (
      <span className="mkt-libcard__mosaic mkt-libcard__mosaic--empty" aria-hidden="true">
        <svg
          viewBox="0 0 24 24"
          width="40"
          height="40"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
        >
          <rect x="3" y="5" width="18" height="14" rx="2" />
          <path d="M10 9.5v5l4.5-2.5z" fill="currentColor" stroke="none" />
        </svg>
      </span>
    );
  }
  if (ids.length < 4) {
    return (
      <span className="mkt-libcard__mosaic mkt-libcard__mosaic--single" aria-hidden="true">
        <MosaicTile id={ids[0]} />
      </span>
    );
  }
  const tiles = [0, 1, 2, 3].map((i) => ids[i] ?? null);
  return (
    <span className="mkt-libcard__mosaic" aria-hidden="true">
      {tiles.map((id, i) => (id ? <MosaicTile key={id} id={id} /> : <span key={`e${i}`} />))}
    </span>
  );
}

function MosaicTile({ id }: { id: string }) {
  const [broken, setBroken] = useState(false);
  if (broken) return <span />;
  return (
    <img
      src={`/api/videos/${encodeURIComponent(id)}/poster`}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  );
}

export function Libraries() {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const pins = usePins(user?.id);
  const {
    data: items,
    err,
    loading,
    reload,
  } = usePageData<LibrarySummary[]>("libraries", fetchLibrarySummary, [t]);

  return (
    <section className="mkt-page mkt-libraries">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("libraries.title")}</h1>
        </TitleRow>
        <Link className="mkt-btn mkt-btn--ghost" to="/videos">
          {t("libraries.allVideos")}
        </Link>
      </header>

      {err != null && <LoadError error={err} onRetry={reload} />}

      {pins && pins.length > 0 && (
        <section className="mkt-libraries__pinned" aria-labelledby="mkt-libraries-pinned">
          <div className="mkt-libraries__pinned-head">
            <h2 id="mkt-libraries-pinned">{t("libraries.pinnedCount", { n: n(pins.length) })}</h2>
            <Link
              className="mkt-libraries__reorder"
              to="/library/pinned"
              aria-label={t("libraries.reorder.label")}
            >
              {t("libraries.reorder")}
            </Link>
          </div>
          <ol className="mkt-pinstrip">
            {pins.map((p) => (
              <li key={p.id}>
                <Link to={`/library/${encodeURIComponent(p.id)}`} className="mkt-pinstrip__chip">
                  <bdi>{p.name}</bdi>
                </Link>
              </li>
            ))}
          </ol>
        </section>
      )}

      <Loading loading={loading}>
        <CardGridPlaceholder heading="mkt-libraries__all" />
      </Loading>

      {items && items.length === 0 && !err && (
        <EmptyState
          kind="first_run"
          title={t("libraries.empty.title")}
          description={user?.is_admin ? t("libraries.empty.descAdmin") : t("libraries.empty.desc")}
          action={
            SERVER_PAGES && user?.is_admin ? (
              <Link className="mkt-btn mkt-btn--primary" to="/admin/libraries">
                {t("home.empty.manage")}
              </Link>
            ) : undefined
          }
        />
      )}

      {items && items.length > 0 && (
        <>
          <h2 className="mkt-libraries__all">{t("libraries.all")}</h2>
          <ul className="mkt-libgrid">
            {items.map((l) => (
              <li
                key={l.id}
                className={`mkt-libcard${pins?.some((p) => p.id === l.id) ? " mkt-libcard--pinned" : ""}`}
              >
                <Link to={`/library/${encodeURIComponent(l.id)}`} className="mkt-libcard__link">
                  <PosterMosaic ids={l.poster_video_ids} />
                  <bdi className="mkt-libcard__name">{l.name}</bdi>
                  <span className="mkt-libcard__meta">
                    {t("libraries.videos", { n: n(l.videos ?? 0) })}
                  </span>
                </Link>
                <PinToggle lib={{ id: l.id, name: l.name }} className="mkt-libcard__pin" />
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
