// Epic 45 Phase 2 (story 45.10) — Playlists (`/playlists`): the user's own
// lists of videos, across their libraries (GET /api/me/playlists): a card per
// playlist (a poster mosaic, the name, how many videos and how long), and a
// form to make a new, empty one. Videos go in from a video's page (Add to
// playlist) or from a selection on the grid.
import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  createPlaylist,
  fetchPlaylists,
  PLAYLIST_NAME_MAX,
  type PlaylistSummary,
} from "../../lib/me";
import { TitleRow } from "../../help/HelpLink";
import { PosterMosaic } from "../Library/Libraries";
import { formatDuration } from "../../components/VideoViews";

export function Playlists() {
  const { t, n } = useI18n();
  const toast = useToast();
  const navigate = useNavigate();
  const [items, setItems] = useState<PlaylistSummary[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    fetchPlaylists()
      .then((r) => !cancelled && setItems(r))
      .catch((e) => {
        if (!cancelled) setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
      });
    return () => {
      cancelled = true;
    };
  }, [reload, t]);

  async function create() {
    const nm = name.trim();
    if (!nm) return;
    setCreating(true);
    try {
      const p = await createPlaylist(nm);
      setName("");
      navigate(`/playlists/${encodeURIComponent(p.id)}`);
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("playlists.createError")}: ${
          e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error")
        }`,
      });
    } finally {
      setCreating(false);
    }
  }

  return (
    <section className="mkt-page mkt-playlists">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("playlists.title")}</h1>
        </TitleRow>
        <form
          className="mkt-playlists__new"
          onSubmit={(e) => {
            e.preventDefault();
            void create();
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
          <Button type="submit" disabled={creating || !name.trim()} loading={creating}>
            {t("playlists.create")}
          </Button>
        </form>
      </header>

      {err && (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={err}
          action={<Button onClick={() => setReload((x) => x + 1)}>{t("common.retry")}</Button>}
        />
      )}

      {!items && !err && (
        <ul className="mkt-libgrid" aria-busy="true">
          {Array.from({ length: 3 }, (_, i) => (
            <li key={i} className="mkt-libcard">
              <Skeleton variant="rect" height={140} />
            </li>
          ))}
        </ul>
      )}

      {items && items.length === 0 && (
        <EmptyState
          kind="first_run"
          title={t("playlists.empty.title")}
          description={t("playlists.empty.desc")}
        />
      )}

      {items && items.length > 0 && (
        <ul className="mkt-libgrid">
          {items.map((p) => (
            <li key={p.id} className="mkt-libcard">
              <Link to={`/playlists/${encodeURIComponent(p.id)}`} className="mkt-libcard__link">
                <PosterMosaic ids={p.poster_video_ids} />
                <bdi className="mkt-libcard__name">{p.name}</bdi>
                <span className="mkt-libcard__meta mkt-muted">
                  {t("libraries.videos", { n: n(p.videos) })}
                  {p.duration_sec > 0 && ` · ${formatDuration(p.duration_sec)}`}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
