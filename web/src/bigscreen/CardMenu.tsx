// Epic 58 story 58.08 (Epic 56 §6.5) — a video card's actions, opened with a
// long OK or the menu key: Play next, Add to queue, Add to playlist, Go to
// series, Mark watched / unwatched. A dialog (a focus layer): Back closes it
// and focus returns to the card.
import { useEffect, useState } from "react";
import { useI18n } from "../lib/i18n";
import { addToQueue } from "../lib/playQueue";
import { addToPlaylist, fetchPlaylists, markWatched, type PlaylistSummary } from "../lib/me";
import type { CardActions } from "./cards";
import { bigPath, useBigNav } from "./nav";
import { Dialog } from "./ui";

export function CardMenu({
  card,
  onClose,
  onDone,
}: {
  card: CardActions;
  onClose: () => void;
  /** what happened, said in the frame's status line */
  onDone: (message: string) => void;
}) {
  const { t } = useI18n();
  const { go } = useBigNav();
  const [picking, setPicking] = useState(false);
  const id = card.videoId!;

  const run = async (work: () => Promise<unknown>, ok: string, fail: string) => {
    onClose();
    try {
      await work();
      onDone(ok);
    } catch {
      onDone(fail);
    }
  };

  if (picking) return <PlaylistPicker videoId={id} onClose={onClose} onDone={onDone} />;

  return (
    <Dialog title={<bdi>{card.title}</bdi>} onClose={onClose} testId="big-card-menu">
      <button
        type="button"
        className="bs-btn"
        data-bs-focus="menu:next"
        data-bs-autofocus
        onClick={() =>
          void run(() => addToQueue([id], "next"), t("playQueue.added"), t("playQueue.error.load"))
        }
      >
        {t("playQueue.playNext")}
      </button>
      <button
        type="button"
        className="bs-btn"
        data-bs-focus="menu:queue"
        onClick={() =>
          void run(() => addToQueue([id], "end"), t("playQueue.added"), t("playQueue.error.load"))
        }
      >
        {t("playQueue.add")}
      </button>
      <button
        type="button"
        className="bs-btn"
        data-bs-focus="menu:playlist"
        onClick={() => setPicking(true)}
      >
        {t("playlists.add")}
      </button>
      {card.seriesId && (
        <button
          type="button"
          className="bs-btn"
          data-bs-focus="menu:series"
          onClick={() => {
            onClose();
            go(bigPath.series(card.seriesId!));
          }}
        >
          {t("series.goTo")}
        </button>
      )}
      <button
        type="button"
        className="bs-btn"
        data-bs-focus="menu:watched"
        onClick={() =>
          void run(
            () => markWatched([id], !card.watched),
            t(card.watched ? "video.markedUnwatched" : "video.markedWatched"),
            t("big.menu.failed")
          )
        }
      >
        {t(card.watched ? "big.menu.markUnwatched" : "video.markWatched")}
      </button>
    </Dialog>
  );
}

/** Add to playlist: the user's manual playlists (a smart one fills itself). */
function PlaylistPicker({
  videoId,
  onClose,
  onDone,
}: {
  videoId: string;
  onClose: () => void;
  onDone: (message: string) => void;
}) {
  const { t, n } = useI18n();
  const [lists, setLists] = useState<PlaylistSummary[] | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    fetchPlaylists()
      .then((l) => live && setLists(l.filter((p) => p.kind === "manual")))
      .catch(() => live && setFailed(true));
    return () => {
      live = false;
    };
  }, []);
  return (
    <Dialog
      title={t("playlists.add")}
      body={
        failed
          ? t("playlists.addError")
          : lists && lists.length === 0
            ? t("playlists.empty.title")
            : undefined
      }
      onClose={onClose}
      testId="big-playlist-picker"
    >
      {(lists ?? []).map((p, i) => (
        <button
          key={p.id}
          type="button"
          className="bs-btn"
          data-bs-focus={`pick:${p.id}`}
          data-bs-autofocus={i === 0 || undefined}
          onClick={async () => {
            onClose();
            try {
              await addToPlaylist(p.id, [videoId]);
              onDone(t("playlists.added", { n: n(1), name: p.name }));
            } catch {
              onDone(t("playlists.addError"));
            }
          }}
        >
          <bdi>{p.name}</bdi>
        </button>
      ))}
      <button type="button" className="bs-btn" data-bs-focus="pick:cancel" onClick={onClose}>
        {t("common.cancel")}
      </button>
    </Dialog>
  );
}
