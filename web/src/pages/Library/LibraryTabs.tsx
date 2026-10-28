// Epic 45 Phase 4 (story 45.24) — a library page's Series and Collections tabs.
//
//   Series (?tab=series&by=name|newest|recent): a poster card per series with
//     a video here, its episodes and how far the user is through them; the sort
//     is in the URL. 60 at a time, Load more for the next.
//   Collections (?tab=collections&kind=topic|filler|standalone|curated): the
//     pipeline's automatic groups (topic collections, filler clips, stand-alone
//     videos: the collections Plex gets) and the admins' curated ones, under one
//     heading per kind, with a chip per kind and its count to keep one. Admins
//     get New collection; an automatic one is read-only, marked Automatic.
//
// Each list is kept for the user (usePageData): coming back shows it at once.
import { useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Chip } from "@ds/components/Chip/Chip";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { usePageData } from "../../lib/usePageData";
import {
  COLLECTION_FILTERS,
  fetchLibraryCollections,
  fetchLibrarySeries,
  filterOf,
  libraryCollectionPath,
  SERIES_SORTS,
  type CollectionFilter,
  type CollectionsPage,
  type LibraryCollection,
  type LibrarySeries,
  type Paged,
  type SeriesSort,
} from "../../lib/libraryTabs";
import { LoadError } from "../../components/LoadError";
import { CardGridPlaceholder, Loading } from "../../components/Placeholders";
import { PosterMosaic } from "./Libraries";
import { SeriesPoster } from "./SeriesBrowser";
import { NewCollectionForm } from "../Collections/Collections";

/** The cache key of a library's collections of a kind: the Overview row reads "all". */
export const collectionsKey = (libraryId: string, kind: CollectionFilter | "all") =>
  `library-collections:${libraryId}:${kind}`;

/** Load more: the next page appended to the kept list (a stale answer is dropped). */
function useLoadMore<T, P extends Paged<T>>(
  data: P | null,
  setData: (f: (prev: P | null) => P | null) => void,
  fetchNext: (offset: number) => Promise<P>
) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>(null);
  async function more() {
    if (!data || data.next == null) return;
    const from = data.next;
    setBusy(true);
    setErr(null);
    try {
      const r = await fetchNext(from);
      setData((prev) =>
        prev && prev.next === from ? { ...r, items: [...prev.items, ...r.items] } : prev
      );
    } catch (e) {
      setErr(e);
    } finally {
      setBusy(false);
    }
  }
  return { busy, err, more };
}

function LoadMore({
  shown,
  next,
  busy,
  onMore,
}: {
  shown: number;
  next: number | null;
  busy: boolean;
  onMore: () => void;
}) {
  const { t, n } = useI18n();
  if (next == null) return null;
  return (
    <div className="mkt-loadmore">
      <Button variant="secondary" loading={busy} onClick={onMore}>
        {t("common.loadMore")} {n(shown)}
      </Button>
    </div>
  );
}

// ---- Series ----

export function SeriesTab({ libraryId }: { libraryId: string }) {
  const { t, n } = useI18n();
  const [params, setParams] = useSearchParams();
  const raw = params.get("by") as SeriesSort | null;
  const sort: SeriesSort = raw && SERIES_SORTS.includes(raw) ? raw : "name";
  const { data, err, loading, reload, setData } = usePageData<Paged<LibrarySeries>>(
    `library-series:${libraryId}:${sort}`,
    () => fetchLibrarySeries(libraryId, sort),
    [t]
  );
  const more = useLoadMore(data, setData, (offset) => fetchLibrarySeries(libraryId, sort, offset));

  function changeSort(next: SeriesSort) {
    const p = new URLSearchParams(params);
    if (next === "name") p.delete("by");
    else p.set("by", next);
    setParams(p, { replace: true });
  }

  return (
    <div className="mkt-library-tab mkt-library-series">
      <div className="mkt-library-tab__bar">
        <label className="mkt-series-browser__sort">
          {t("series.sort")}
          <select
            value={sort}
            onChange={(e) => changeSort(e.target.value as SeriesSort)}
            aria-label={t("series.sort")}
          >
            {SERIES_SORTS.map((s) => (
              <option key={s} value={s}>
                {t(`libraryPage.series.sort.${s}`)}
              </option>
            ))}
          </select>
        </label>
        {data && (
          <span className="mkt-muted">{t("libraryPage.series.count", { n: n(data.total) })}</span>
        )}
      </div>

      {err != null && <LoadError error={err} onRetry={reload} />}
      <Loading loading={loading}>
        <SeriesGridPlaceholder />
      </Loading>
      {data && data.items.length === 0 && (
        <EmptyState title={t("series.empty.title")} description={t("libraryPage.series.empty")} />
      )}
      {data && data.items.length > 0 && (
        <ul className="mkt-series-grid" data-testid="series-grid">
          {data.items.map((s) => (
            <SeriesCard key={s.id} s={s} />
          ))}
        </ul>
      )}
      {more.err != null && <LoadError error={more.err} onRetry={() => void more.more()} />}
      {data && (
        <LoadMore
          shown={data.items.length}
          next={data.next}
          busy={more.busy}
          onMore={() => void more.more()}
        />
      )}
    </div>
  );
}

function SeriesCard({ s }: { s: LibrarySeries }) {
  const { t, n } = useI18n();
  const pct = s.episodes > 0 ? (s.watched / s.episodes) * 100 : 0;
  return (
    <li>
      <Link to={`/series/${encodeURIComponent(s.id)}`} className="mkt-series-card-link">
        <Card interactive className="mkt-series-card">
          <div className="mkt-series-card__poster">
            <SeriesPoster series={s} />
          </div>
          <div className="mkt-series-card__meta">
            <bdi className="mkt-series-card__name">{s.name || t("libraryPage.seriesUnnamed")}</bdi>
            <div className="mkt-series-card__flags">
              <Badge tone="neutral">{t("libraryPage.episodes", { n: n(s.episodes) })}</Badge>
              {s.in_progress > 0 && (
                <Badge tone="accent">{t("libraryPage.series.inProgress")}</Badge>
              )}
            </div>
            <ProgressBar
              value={pct}
              label={t("series.progress.label", { watched: n(s.watched), total: n(s.episodes) })}
            />
          </div>
        </Card>
      </Link>
    </li>
  );
}

/** The Series tab's cards (poster, name, badges, progress) while it loads. */
function SeriesGridPlaceholder() {
  return (
    <ul className="mkt-series-grid" aria-hidden="true">
      {Array.from({ length: 6 }, (_, i) => (
        <li key={i}>
          <Card className="mkt-series-card">
            <div className="mkt-series-card__poster">
              <Skeleton variant="rect" className="mkt-ph-full" />
            </div>
            <div className="mkt-series-card__meta">
              <Skeleton variant="text" className="mkt-ph-line" width="70%" />
              <Skeleton variant="text" className="mkt-ph-line" width="40%" />
              <Skeleton variant="text" className="mkt-ph-line" width="100%" />
            </div>
          </Card>
        </li>
      ))}
    </ul>
  );
}

// ---- Collections ----

/** A collection's card: its mosaic, name, kind and size (a row item or a grid cell). */
export function LibraryCollectionCard({
  libraryId,
  c,
  className,
}: {
  libraryId: string;
  c: LibraryCollection;
  className: string;
}) {
  const { t, n } = useI18n();
  return (
    <li className={className}>
      <Link to={libraryCollectionPath(libraryId, c)} className="mkt-libcard__link">
        <PosterMosaic ids={c.poster_video_ids} />
        <bdi className="mkt-libcard__name">{c.name}</bdi>
        <span className="mkt-libcard__meta mkt-collection-card__meta">
          {c.kind === "smart" ? (
            <Badge tone="accent">{t("collections.smart")}</Badge>
          ) : (
            <span>{t("libraries.videos", { n: n(c.videos ?? 0) })}</span>
          )}
          {c.automatic && (
            <Badge tone="neutral" title={t("collections.automatic.hint")}>
              {t("collections.automatic")}
            </Badge>
          )}
        </span>
      </Link>
    </li>
  );
}

export function CollectionsTab({ libraryId }: { libraryId: string }) {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const [params, setParams] = useSearchParams();
  const raw = params.get("kind") as CollectionFilter | null;
  const kind: CollectionFilter | "all" = raw && COLLECTION_FILTERS.includes(raw) ? raw : "all";
  const { data, err, loading, reload, setData } = usePageData<CollectionsPage>(
    collectionsKey(libraryId, kind),
    () => fetchLibraryCollections(libraryId, kind),
    [t]
  );
  const more = useLoadMore(data, setData, (offset) =>
    fetchLibraryCollections(libraryId, kind, offset)
  );
  // the chips keep the last counts while another kind loads
  const lastCounts = useRef<CollectionsPage["counts"] | null>(null);
  if (data) lastCounts.current = data.counts;
  const counts = data?.counts ?? lastCounts.current;

  function pick(next: CollectionFilter | "all") {
    const p = new URLSearchParams(params);
    if (next === "all") p.delete("kind");
    else p.set("kind", next);
    setParams(p, { replace: true });
  }

  // the list comes ordered by kind: a heading where the kind changes
  const groups: { kind: CollectionFilter; items: LibraryCollection[] }[] = [];
  for (const c of data?.items ?? []) {
    const k = filterOf(c);
    const last = groups[groups.length - 1];
    if (last && last.kind === k) last.items.push(c);
    else groups.push({ kind: k, items: [c] });
  }

  return (
    <div className="mkt-library-tab mkt-library-collections">
      {user?.is_admin && <NewCollectionForm libraryId={libraryId} />}

      {counts && counts.all > 0 && (
        <div className="mkt-chips" role="group" aria-label={t("collections.kindFilter")}>
          {(["all", ...COLLECTION_FILTERS] as const).map((k) => (
            <button
              key={k}
              type="button"
              className="mkt-chip-btn"
              aria-pressed={kind === k}
              onClick={() => pick(k)}
            >
              <Chip selected={kind === k}>
                {t(`collections.kind.${k}`)} <span className="mkt-chip-count">{n(counts[k])}</span>
              </Chip>
            </button>
          ))}
        </div>
      )}

      {err != null && <LoadError error={err} onRetry={reload} />}
      <Loading loading={loading}>
        <CardGridPlaceholder cards={4} heading="mkt-library-collections__heading" />
      </Loading>

      {data && data.counts.all === 0 && (
        <EmptyState
          kind="first_run"
          title={t("collections.empty.title")}
          description={
            user?.is_admin ? t("collections.empty.descAdmin") : t("collections.empty.desc")
          }
        />
      )}
      {data && data.counts.all > 0 && data.items.length === 0 && (
        <EmptyState title={t("collections.emptyKind")} />
      )}

      {groups.map((g, i) => (
        <section
          key={`${g.kind}:${i}`}
          className="mkt-library-collections__group"
          aria-label={t(`collections.kind.${g.kind}`)}
          data-testid={`collections-${g.kind}`}
        >
          {kind === "all" && (
            <h2 className="mkt-library-collections__heading">
              {t(`collections.kind.${g.kind}`)}{" "}
              <span className="mkt-muted">{n(data?.counts[g.kind] ?? g.items.length)}</span>
            </h2>
          )}
          <ul className="mkt-libgrid">
            {g.items.map((c) => (
              <LibraryCollectionCard
                key={`${c.kind}:${c.id}`}
                libraryId={libraryId}
                c={c}
                className="mkt-libcard"
              />
            ))}
          </ul>
        </section>
      ))}
      {more.err != null && <LoadError error={more.err} onRetry={() => void more.more()} />}
      {data && (
        <LoadMore
          shown={data.items.length}
          next={data.next}
          busy={more.busy}
          onMore={() => void more.more()}
        />
      )}
    </div>
  );
}
