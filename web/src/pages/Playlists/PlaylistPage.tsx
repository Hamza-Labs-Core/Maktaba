// Epic 45 Phase 2 (stories 45.10, 45.13) — one playlist (`/playlists/:id`):
// its videos in order (GET /api/me/playlists/{id}); Play all, Shuffle and Play
// from here (the play queue, lib/playQueue); move a video up or down (PUT
// …/order: buttons, so the keyboard reorders too); remove one; rename the
// playlist; delete it after a confirmation. Another user's playlist answers
// 404, the same as one that does not exist.
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { watchedFraction } from "../../lib/browse";
import {
  deletePlaylist,
  fetchPlaylist,
  PLAYLIST_NAME_MAX,
  removeFromPlaylist,
  reorderPlaylist,
  updatePlaylist,
  type PlaylistDetail,
} from "../../lib/me";
import { shuffled, startQueue, watchPath } from "../../lib/playQueue";
import { TitleRow } from "../../help/HelpLink";
import { VideoPoster, formatDuration } from "../../components/VideoViews";
import { LoadError } from "../../components/LoadError";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { ItemListPlaceholder, Loading } from "../../components/Placeholders";

export function PlaylistPage() {
  const { playlistId = "" } = useParams();
  return <PlaylistOf key={playlistId} id={playlistId} />;
}

function PlaylistOf({ id }: { id: string }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const navigate = useNavigate();
  const {
    data: doc,
    err: loadErr,
    loading,
    reload,
    setData: setDoc,
  } = usePageData<PlaylistDetail>(`playlist:${id}`, () => fetchPlaylist(id), [t]);
  const notFound = loadErr instanceof ApiError && loadErr.status === 404;
  const err = notFound ? null : loadErr;
  const [busy, setBusy] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const fail = (e: unknown) =>
    toast.show({
      tone: "error",
      message: `${t("playlists.changeError")}: ${errorText(e, t)}`,
    });

  if (notFound) {
    return (
      <section className="mkt-page mkt-playlist">
        <ErrorState
          kind="not_found"
          title={t("playlists.notFound")}
          action={
            <Link className="mkt-btn mkt-btn--primary" to="/playlists">
              {t("playlists.back")}
            </Link>
          }
        />
      </section>
    );
  }
  if (err != null) {
    return (
      <section className="mkt-page mkt-playlist">
        <LoadError error={err} onRetry={reload} />
      </section>
    );
  }
  if (!doc) {
    return (
      <section className="mkt-page mkt-playlist">
        <Loading loading={loading}>
          <ItemListPlaceholder />
        </Loading>
      </section>
    );
  }

  const { playlist, items } = doc;
  const ids = items.map((v) => v.id);

  function playFrom(index: number, shuffle = false) {
    const list = shuffle ? shuffled(ids) : ids;
    const first = startQueue(list, shuffle ? 0 : index);
    if (first) navigate(watchPath(first), { state: { autoplay: true } });
  }

  async function move(index: number, delta: number) {
    const to = index + delta;
    if (to < 0 || to >= ids.length) return;
    const order = ids.slice();
    [order[index], order[to]] = [order[to]!, order[index]!];
    setBusy(true);
    try {
      setDoc(await reorderPlaylist(id, order));
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function remove(videoId: string) {
    setBusy(true);
    try {
      await removeFromPlaylist(id, videoId);
      setDoc((d) =>
        d
          ? {
              playlist: { ...d.playlist, videos: Math.max(0, d.playlist.videos - 1) },
              items: d.items.filter((v) => v.id !== videoId),
            }
          : d
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function rename() {
    const nm = name.trim();
    if (!nm || nm === playlist.name) {
      setRenaming(false);
      return;
    }
    setBusy(true);
    try {
      const p = await updatePlaylist(id, { name: nm });
      setDoc((d) => (d ? { ...d, playlist: { ...d.playlist, name: p.name || nm } } : d));
      setRenaming(false);
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function removePlaylist() {
    setBusy(true);
    try {
      await deletePlaylist(id);
      toast.show({ tone: "success", message: t("playlists.deleted", { name: playlist.name }) });
      navigate("/playlists");
    } catch (e) {
      fail(e);
      setBusy(false);
    }
  }

  return (
    <section className="mkt-page mkt-playlist">
      <header className="mkt-page__header">
        {renaming ? (
          <form
            className="mkt-playlist__rename"
            onSubmit={(e) => {
              e.preventDefault();
              void rename();
            }}
          >
            <label className="mkt-field">
              <span className="mkt-field__label">{t("playlists.name")}</span>
              <input
                type="text"
                dir="auto"
                value={name}
                maxLength={PLAYLIST_NAME_MAX}
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
              <bdi>{playlist.name}</bdi>
            </h1>
          </TitleRow>
        )}
        <span className="mkt-muted">
          {t("libraries.videos", { n: n(items.length) })}
          {playlist.duration_sec > 0 && ` · ${formatDuration(playlist.duration_sec)}`}
        </span>
        {playlist.description && (
          <p className="mkt-muted" dir="auto">
            {playlist.description}
          </p>
        )}
        <div className="mkt-playlist__actions">
          <Button onClick={() => playFrom(0)} disabled={items.length === 0}>
            ▶ {t("browse.playAll")}
          </Button>
          <Button
            variant="secondary"
            onClick={() => playFrom(0, true)}
            disabled={items.length === 0}
          >
            ⇄ {t("browse.shuffle")}
          </Button>
          {!renaming && (
            <Button
              variant="ghost"
              onClick={() => {
                setName(playlist.name);
                setRenaming(true);
              }}
            >
              {t("playlists.rename")}
            </Button>
          )}
          <Button variant="destructive" onClick={() => setConfirmDelete(true)}>
            {t("playlists.delete")}
          </Button>
        </div>
      </header>

      {items.length === 0 ? (
        <EmptyState
          title={t("playlists.emptyList.title")}
          description={t("playlists.emptyList.desc")}
        />
      ) : (
        <ol className="mkt-plitems">
          {items.map((v, i) => {
            const title = v.title || v.filename || v.id;
            const frac = watchedFraction(v);
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
                    {v.completed
                      ? ` · ${t("browse.watchedMark")}`
                      : frac !== null
                        ? ` · ${t("home.watchedPct", { pct: n(Math.round(frac * 100)) })}`
                        : ""}
                  </span>
                </span>
                <span className="mkt-plitems__actions">
                  <Button
                    size="sm"
                    variant="secondary"
                    aria-label={t("playlists.playFromNamed", { name: title })}
                    onClick={() => playFrom(i)}
                  >
                    ▶ {t("playlists.playFrom")}
                  </Button>
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
                    aria-label={t("playlists.removeNamed", { name: title })}
                    disabled={busy}
                    onClick={() => void remove(v.id)}
                  >
                    <span aria-hidden="true">✕</span>
                  </button>
                </span>
              </li>
            );
          })}
        </ol>
      )}

      <Modal
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title={t("playlists.deleteTitle", { name: playlist.name })}
        dismissable={false}
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirmDelete(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" disabled={busy} onClick={() => void removePlaylist()}>
              {t("playlists.delete")}
            </Button>
          </>
        }
      >
        <p>{t("playlists.deleteDesc")}</p>
      </Modal>
    </section>
  );
}
