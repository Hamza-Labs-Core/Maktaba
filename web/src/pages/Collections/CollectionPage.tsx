// Epic 45 Phase 3 (story 45.17) — one collection (`/collections/:id`).
//
//   Manual: its videos in the admin's order; Play all, Shuffle and Play from
//     here (the play queue). Admins move a video up or down (buttons, so the
//     keyboard reorders too), remove one, rename the collection or delete it
//     after a confirmation. Videos go in from the Browse tab (Select, then
//     Add to collection).
//   Smart (a saved filter): what it filters on, Show the videos (the library's
//     Browse tab with those filters), Play all and Shuffle; admins edit the
//     filters there (Save filters to the collection), rename or delete it.
//
// A collection in a library the user may not see answers 404, the same as
// one that does not exist.
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Chip } from "@ds/components/Chip/Chip";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import type { VideoCard } from "../../lib/browse";
import { fetchFacets } from "../../lib/me";
import {
  browsePathFor,
  COLLECTION_NAME_MAX,
  deleteCollection,
  fetchCollection,
  fetchCollectionItems,
  removeFromCollection,
  renameCollection,
  reorderCollection,
  type Collection,
} from "../../lib/collections";
import { shuffled, startQueue, startQueueFromQuery, watchPath } from "../../lib/playQueue";
import { TitleRow } from "../../help/HelpLink";
import { VideoPoster, formatDuration } from "../../components/VideoViews";
import { LoadError } from "../../components/LoadError";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { ItemListPlaceholder, Loading } from "../../components/Placeholders";

export function CollectionPage() {
  const { collectionId = "" } = useParams();
  return <CollectionOf key={collectionId} id={collectionId} />;
}

function CollectionOf({ id }: { id: string }) {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const admin = !!user?.is_admin;
  const toast = useToast();
  const navigate = useNavigate();
  const {
    data,
    err: loadErr,
    loading,
    reload,
    setData,
  } = usePageData<{ col: Collection; items: VideoCard[] }>(
    `collection:${id}`,
    async () => {
      const col = await fetchCollection(id);
      const items = col.is_smart ? [] : await fetchCollectionItems(id);
      return { col, items };
    },
    [t]
  );
  const col = data?.col ?? null;
  const items = data?.items ?? null;
  const setCol = (c: Collection) => setData((d) => (d ? { ...d, col: c } : d));
  const setItems = (f: (vs: VideoCard[]) => VideoCard[]) =>
    setData((d) => (d ? { ...d, items: f(d.items) } : d));
  const notFound =
    loadErr instanceof ApiError && (loadErr.status === 404 || loadErr.status === 400);
  const err = notFound ? null : loadErr;
  const [busy, setBusy] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  // a saved filter's tag is an id: its name comes from the library's facets
  const [tagNames, setTagNames] = useState<Record<string, string>>({});

  useEffect(() => {
    const tag = col?.filters?.tag;
    if (!col || !tag) return;
    let cancelled = false;
    fetchFacets(col.library_id)
      .then((f) => {
        if (!cancelled) setTagNames(Object.fromEntries(f.tags.map((x) => [x.id, x.name])));
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [col]);

  const fail = (e: unknown) =>
    toast.show({
      tone: "error",
      message: `${t("collections.changeError")}: ${errorText(e, t)}`,
    });

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
  if (err != null) {
    return (
      <section className="mkt-page mkt-collection">
        <LoadError error={err} onRetry={reload} />
      </section>
    );
  }
  if (!col || !items) {
    return (
      <section className="mkt-page mkt-collection">
        <Loading loading={loading}>
          <ItemListPlaceholder />
        </Loading>
      </section>
    );
  }

  const ids = items.map((v) => v.id);
  // a saved filter plays what the grid lists: its filters over its library
  const filterQuery = () =>
    new URLSearchParams({ ...(col.filters ?? {}), library: col.library_id }).toString();

  async function play(index: number, shuffle = false) {
    if (col?.is_smart) {
      setBusy(true);
      try {
        const first = await startQueueFromQuery(filterQuery(), shuffle);
        if (first) navigate(watchPath(first), { state: { autoplay: true } });
        else toast.show({ tone: "info", message: t("browse.play.nothing") });
      } catch (e) {
        fail(e);
      } finally {
        setBusy(false);
      }
      return;
    }
    const list = shuffle ? shuffled(ids) : ids;
    const first = startQueue(list, shuffle ? 0 : index);
    if (first) navigate(watchPath(first), { state: { autoplay: true } });
  }

  async function move(index: number, delta: number) {
    const to = index + delta;
    if (!items || to < 0 || to >= items.length) return;
    const next = items.slice();
    [next[index], next[to]] = [next[to]!, next[index]!];
    setBusy(true);
    try {
      await reorderCollection(
        id,
        next.map((v) => v.id)
      );
      setItems(() => next);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function remove(videoId: string) {
    setBusy(true);
    try {
      await removeFromCollection(id, videoId);
      setItems((vs) => vs.filter((v) => v.id !== videoId));
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function rename() {
    const nm = name.trim();
    if (!col || !nm || nm === col.name) {
      setRenaming(false);
      return;
    }
    setBusy(true);
    try {
      await renameCollection(id, nm);
      setCol({ ...col, name: nm });
      setRenaming(false);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function removeCollection() {
    if (!col) return;
    setBusy(true);
    try {
      await deleteCollection(id);
      toast.show({ tone: "success", message: t("collections.deleted", { name: col.name }) });
      navigate(`/library/${encodeURIComponent(col.library_id)}/collections`);
    } catch (e) {
      fail(e);
      setBusy(false);
    }
  }

  const filterChips = Object.entries(col.filters ?? {}).filter(
    ([k]) => k !== "sort" && k !== "order"
  );
  const empty = !col.is_smart && items.length === 0;

  return (
    <section className="mkt-page mkt-collection">
      <Link className="mkt-crumb" to={`/library/${encodeURIComponent(col.library_id)}/collections`}>
        {t("collections.title")}
      </Link>
      <header className="mkt-page__header mkt-collection__header">
        {renaming ? (
          <form
            className="mkt-playlist__rename"
            onSubmit={(e) => {
              e.preventDefault();
              void rename();
            }}
          >
            <label className="mkt-field">
              <span className="mkt-field__label">{t("collections.name")}</span>
              <input
                type="text"
                dir="auto"
                value={name}
                maxLength={COLLECTION_NAME_MAX}
                onChange={(e) => setName(e.target.value)}
                autoFocus
              />
            </label>
            <Button type="submit" disabled={busy || !name.trim()}>
              {t("common.save")}
            </Button>
            <Button variant="ghost" onClick={() => setRenaming(false)}>
              {t("common.cancel")}
            </Button>
          </form>
        ) : (
          <TitleRow>
            <h1>
              <bdi>{col.name}</bdi>
            </h1>
          </TitleRow>
        )}
        <span className="mkt-muted">
          {col.is_smart
            ? t("collections.smartDesc")
            : t("libraries.videos", { n: n(items.length) })}
        </span>
        {col.is_smart && filterChips.length > 0 && (
          <ul className="mkt-chips mkt-collection__filters" aria-label={t("collections.filters")}>
            {filterChips.map(([k, v]) => (
              <li key={k}>
                <Chip>
                  {t(`collections.filter.${k}`)}:{" "}
                  <bdi>{k === "tag" ? (tagNames[v] ?? v) : filterLabel(t, k, v)}</bdi>
                </Chip>
              </li>
            ))}
          </ul>
        )}
        <div className="mkt-playlist__actions">
          <Button onClick={() => void play(0)} disabled={busy || empty}>
            ▶ {t("browse.playAll")}
          </Button>
          <Button variant="secondary" onClick={() => void play(0, true)} disabled={busy || empty}>
            ⇄ {t("browse.shuffle")}
          </Button>
          {col.is_smart && (
            <Link className="mkt-btn mkt-btn--secondary" to={browsePathFor(col)}>
              {t("collections.showVideos")}
            </Link>
          )}
          {admin && col.is_smart && (
            <Link
              className="mkt-btn mkt-btn--ghost"
              to={`${browsePathFor(col)}&collection=${encodeURIComponent(col.id)}`}
            >
              {t("collections.editFilters")}
            </Link>
          )}
          {admin && !renaming && (
            <Button
              variant="ghost"
              onClick={() => {
                setName(col.name);
                setRenaming(true);
              }}
            >
              {t("collections.rename")}
            </Button>
          )}
          {admin && (
            <Button variant="destructive" onClick={() => setConfirmDelete(true)}>
              {t("collections.delete")}
            </Button>
          )}
        </div>
      </header>

      {empty && (
        <EmptyState
          title={t("collections.emptyList.title")}
          description={
            admin ? t("collections.emptyList.descAdmin") : t("collections.emptyList.desc")
          }
        />
      )}

      {!col.is_smart && items.length > 0 && (
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
                  </span>
                </span>
                <span className="mkt-plitems__actions">
                  <Button
                    size="sm"
                    variant="secondary"
                    aria-label={t("playlists.playFromNamed", { name: title })}
                    onClick={() => void play(i)}
                  >
                    ▶ {t("playlists.playFrom")}
                  </Button>
                  {admin && (
                    <>
                      <button
                        type="button"
                        className="mkt-btn mkt-btn--ghost"
                        aria-label={t("libraries.moveUp", { name: title })}
                        disabled={busy || i === 0}
                        onClick={() => void move(i, -1)}
                      >
                        <span aria-hidden="true">↑</span>
                      </button>
                      <button
                        type="button"
                        className="mkt-btn mkt-btn--ghost"
                        aria-label={t("libraries.moveDown", { name: title })}
                        disabled={busy || i === items.length - 1}
                        onClick={() => void move(i, 1)}
                      >
                        <span aria-hidden="true">↓</span>
                      </button>
                      <button
                        type="button"
                        className="mkt-btn mkt-btn--ghost"
                        aria-label={t("collections.removeNamed", { name: title })}
                        disabled={busy}
                        onClick={() => void remove(v.id)}
                      >
                        <span aria-hidden="true">✕</span>
                      </button>
                    </>
                  )}
                </span>
              </li>
            );
          })}
        </ol>
      )}

      <Modal
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title={t("collections.deleteTitle", { name: col.name })}
        dismissable={false}
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirmDelete(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" disabled={busy} onClick={() => void removeCollection()}>
              {t("collections.delete")}
            </Button>
          </>
        }
      >
        <p>{t("collections.deleteDesc")}</p>
      </Modal>
    </section>
  );
}

// A saved filter's value as the grid shows it: the bands' and watched states'
// own words; ids (a tag, a series) and free text as they are.
function filterLabel(t: (k: string) => string, key: string, value: string): string {
  if (key === "duration" || key === "added" || key === "resolution")
    return t(`browse.${key}.${value}`);
  if (key === "watched") return t(`videosAll.watched.${value}`);
  return value;
}
