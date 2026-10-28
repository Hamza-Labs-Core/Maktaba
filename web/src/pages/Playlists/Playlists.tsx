// Epic 45 Phase 2 (story 45.10) — Playlists (`/playlists`): the user's own
// lists of videos, across their libraries (GET /api/me/playlists): a card per
// playlist (a poster mosaic, the name, how many videos and how long), and a
// form to make a new, empty one. Videos go in from a video's page (Add to
// playlist) or from a selection on the grid.
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
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
import { LoadError } from "../../components/LoadError";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { CardGridPlaceholder, Loading } from "../../components/Placeholders";

export function Playlists() {
  const { t, n } = useI18n();
  const toast = useToast();
  const navigate = useNavigate();
  const {
    data: items,
    err,
    loading,
    reload,
  } = usePageData<PlaylistSummary[]>("playlists", fetchPlaylists, [t]);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);

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
        message: `${t("playlists.createError")}: ${errorText(e, t)}`,
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

      {err != null && <LoadError error={err} onRetry={reload} />}

      <Loading loading={loading}>
        <CardGridPlaceholder cards={3} />
      </Loading>

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
