// Epic 45 Phase 2 (story 45.10) — "Add to playlist": a dialog listing the
// user's playlists (each with an Add button) and a field to make a new one with
// the videos in it. Used by the video page and by a selection on the grid.
import { useEffect, useState } from "react";
import { Modal } from "@ds/components/Modal/Modal";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import {
  addToPlaylist,
  createPlaylist,
  fetchPlaylists,
  PLAYLIST_NAME_MAX,
  type PlaylistSummary,
} from "../lib/me";

export function AddToPlaylist({
  open,
  videoIds,
  onClose,
  onAdded,
}: {
  open: boolean;
  videoIds: string[];
  onClose: () => void;
  /** after the videos went into a playlist (the dialog closes itself) */
  onAdded?: (playlist: { id: string; name: string }) => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [lists, setLists] = useState<PlaylistSummary[] | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLists(null);
    setLoadErr(null);
    setName("");
    fetchPlaylists()
      .then((ls) => !cancelled && setLists(ls))
      .catch((e) => {
        if (cancelled) return;
        setLists([]);
        setLoadErr(e instanceof ApiError ? e.problem.title : t("common.error"));
      });
    return () => {
      cancelled = true;
    };
  }, [open, t]);

  const fail = (e: unknown) =>
    toast.show({
      tone: "error",
      message: `${t("playlists.addError")}: ${
        e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error")
      }`,
    });

  async function addTo(p: { id: string; name: string }) {
    setBusy(true);
    try {
      const added = await addToPlaylist(p.id, videoIds);
      toast.show({
        tone: "success",
        message: t("playlists.added", { n: n(added), name: p.name }),
      });
      onAdded?.(p);
      onClose();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function createWith() {
    const nm = name.trim();
    if (!nm) return;
    setBusy(true);
    try {
      const p = await createPlaylist(nm, "", videoIds);
      toast.show({
        tone: "success",
        message: t("playlists.added", { n: n(videoIds.length), name: p.name || nm }),
      });
      onAdded?.({ id: p.id, name: p.name || nm });
      onClose();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={t("playlists.addTitle", { n: n(videoIds.length) })}>
      <div className="mkt-addpl">
        {lists === null && <p className="mkt-loading">{t("common.loading")}</p>}
        {loadErr && <p className="mkt-muted">{loadErr}</p>}
        {lists && lists.length > 0 && (
          <ul className="mkt-addpl__list" aria-label={t("playlists.title")}>
            {lists.map((p) => (
              <li key={p.id} className="mkt-addpl__item">
                <bdi className="mkt-addpl__name">{p.name}</bdi>
                <span className="mkt-muted">{t("libraries.videos", { n: n(p.videos) })}</span>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={busy}
                  aria-label={t("playlists.addToNamed", { name: p.name })}
                  onClick={() => void addTo(p)}
                >
                  {t("playlists.addTo")}
                </Button>
              </li>
            ))}
          </ul>
        )}
        {lists && lists.length === 0 && !loadErr && (
          <p className="mkt-muted">{t("playlists.noneYet")}</p>
        )}
        <form
          className="mkt-addpl__new"
          onSubmit={(e) => {
            e.preventDefault();
            void createWith();
          }}
        >
          <label className="mkt-field">
            <span className="mkt-field__label">{t("playlists.newName")}</span>
            <input
              type="text"
              dir="auto"
              value={name}
              maxLength={PLAYLIST_NAME_MAX}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <Button type="submit" variant="primary" disabled={busy || !name.trim()}>
            {t("playlists.createAndAdd")}
          </Button>
        </form>
      </div>
    </Modal>
  );
}
