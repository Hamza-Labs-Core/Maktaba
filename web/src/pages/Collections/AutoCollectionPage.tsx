// Epic 45 Phase 4 (story 45.24) — an automatic collection's page
// (`/library/:libraryId/collections/:kind/:groupId`, kind topic or clip): a
// topic collection or clip group the pipeline made (the collections Plex
// gets), its videos in their stored order, 60 at a time. Read-only: Play all,
// Shuffle and Play from here, never curation (the pipeline rebuilds it). One
// in a library the user may not see answers 404, the same as one that does
// not exist.
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  fetchAutoCollection,
  libraryTabPath,
  type AutoCollectionPage as Page,
} from "../../lib/libraryTabs";
import { shuffled, startQueue, watchPath } from "../../lib/playQueue";
import { TitleRow } from "../../help/HelpLink";
import { VideoPoster, formatDuration } from "../../components/VideoViews";
import { LoadError } from "../../components/LoadError";
import { usePageData } from "../../lib/usePageData";
import { ItemListPlaceholder, Loading } from "../../components/Placeholders";

export function AutoCollectionPage() {
  const { libraryId = "", kind = "", groupId = "" } = useParams();
  const key = `${libraryId}/${kind}/${groupId}`;
  return <AutoCollectionOf key={key} libraryId={libraryId} kind={kind} id={groupId} />;
}

function AutoCollectionOf({
  libraryId,
  kind,
  id,
}: {
  libraryId: string;
  kind: string;
  id: string;
}) {
  const { t, n } = useI18n();
  const navigate = useNavigate();
  const known = kind === "topic" || kind === "clip";
  const { data, err, loading, reload, setData } = usePageData<Page>(
    known ? `collection:${kind}:${libraryId}:${id}` : null,
    () => fetchAutoCollection(libraryId, kind, id),
    [t]
  );
  const [busy, setBusy] = useState(false);
  const [moreErr, setMoreErr] = useState<unknown>(null);
  const notFound =
    !known || (err instanceof ApiError && (err.status === 404 || err.status === 400));
  const tabPath = libraryTabPath(libraryId, "collections");

  if (notFound) {
    return (
      <section className="mkt-page mkt-collection">
        <ErrorState
          kind="not_found"
          title={t("collections.notFound")}
          action={
            <Link className="mkt-btn mkt-btn--primary" to="/library">
              {t("libraryPage.backToLibraries")}
            </Link>
          }
        />
      </section>
    );
  }
  if (err != null && !data) {
    return (
      <section className="mkt-page mkt-collection">
        <LoadError error={err} onRetry={reload} />
      </section>
    );
  }
  if (!data) {
    return (
      <section className="mkt-page mkt-collection">
        <Loading loading={loading}>
          <ItemListPlaceholder />
        </Loading>
      </section>
    );
  }

  const col = data.collection;
  const items = data.items;
  const ids = items.map((v) => v.id);
  const kindLabel = t(
    `collections.kind.${col.kind === "clip" ? (col.subkind ?? "filler") : "topic"}`
  );

  function play(index: number, shuffle = false) {
    const first = startQueue(shuffle ? shuffled(ids) : ids, shuffle ? 0 : index);
    if (first) navigate(watchPath(first), { state: { autoplay: true } });
  }

  async function loadMore() {
    const from = data?.next;
    if (from == null) return;
    setBusy(true);
    setMoreErr(null);
    try {
      const r = await fetchAutoCollection(libraryId, kind, id, from);
      setData((prev) =>
        prev && prev.next === from ? { ...r, items: [...prev.items, ...r.items] } : prev
      );
    } catch (e) {
      setMoreErr(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="mkt-page mkt-collection">
      <Link className="mkt-crumb" to={tabPath}>
        <bdi>{col.library.name}</bdi> · {t("collections.title")}
      </Link>
      <header className="mkt-page__header mkt-collection__header">
        <TitleRow>
          <h1>
            <bdi>{col.name}</bdi>
          </h1>
        </TitleRow>
        <span className="mkt-collection__badges">
          <Badge tone="neutral" title={t("collections.automatic.hint")}>
            {t("collections.automatic")}
          </Badge>
          <Badge tone="accent">{kindLabel}</Badge>
          <span className="mkt-muted">{t("libraries.videos", { n: n(data.total) })}</span>
        </span>
        <p className="mkt-muted mkt-collection__note">{t("collections.automatic.desc")}</p>
        <div className="mkt-playlist__actions">
          <Button onClick={() => play(0)} disabled={ids.length === 0}>
            ▶ {t("browse.playAll")}
          </Button>
          <Button variant="secondary" onClick={() => play(0, true)} disabled={ids.length === 0}>
            ⇄ {t("browse.shuffle")}
          </Button>
        </div>
      </header>

      <ol className="mkt-plitems">
        {items.map((v, i) => {
          const title = v.title || v.filename || v.id;
          return (
            <li key={v.id} className="mkt-plitems__item">
              <span className="mkt-plitems__num">{n(i + 1)}</span>
              <Link to={`/videos/${encodeURIComponent(v.id)}`} className="mkt-plitems__art">
                <VideoPoster videoId={v.id} />
              </Link>
              <span className="mkt-plitems__text">
                <Link to={`/videos/${encodeURIComponent(v.id)}`}>
                  <bdi>{title}</bdi>
                </Link>
                <span className="mkt-muted">
                  {typeof v.duration_sec === "number" && v.duration_sec > 0
                    ? formatDuration(v.duration_sec)
                    : ""}
                  {v.completed ? ` · ✓ ${t("collections.watched")}` : ""}
                </span>
              </span>
              <span className="mkt-plitems__actions">
                <Button
                  size="sm"
                  variant="secondary"
                  aria-label={t("playlists.playFromNamed", { name: title })}
                  onClick={() => play(i)}
                >
                  ▶ {t("playlists.playFrom")}
                </Button>
              </span>
            </li>
          );
        })}
      </ol>
      {moreErr != null && <LoadError error={moreErr} onRetry={() => void loadMore()} />}
      {data.next != null && (
        <div className="mkt-loadmore">
          <Button variant="secondary" loading={busy} onClick={() => void loadMore()}>
            {t("common.loadMore")} {n(items.length)}
          </Button>
        </div>
      )}
    </section>
  );
}
