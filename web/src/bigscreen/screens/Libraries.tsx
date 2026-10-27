// Epic 46 — big screen's Libraries (a card per library the user may see:
// GET /api/libraries/summary) and a library (its hub's rows, GET
// /api/libraries/{id}/hub, then all its videos newest first, GET /api/videos,
// with Load more). A library the user may not see is the API's 404.
import { useCallback, useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  fetchHub,
  fetchLibrarySummary,
  type LibraryHub,
  type LibrarySummary,
  type VideoCard,
} from "../../lib/browse";
import { bigPath, useBigNav } from "../nav";
import { Empty, Failed, Grid, Loading, Picture, Row, Screen, VideoTile, posterOf } from "../ui";
import { errorText } from "../../lib/errorText";

export function BigLibraries() {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const [libs, setLibs] = useState<LibrarySummary[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    fetchLibrarySummary()
      .then((l) => !cancelled && setLibs(l))
      .catch((e) => !cancelled && setErr(errorText(e, t)));
    return () => {
      cancelled = true;
    };
  }, [reload, t]);

  if (err) return <Failed message={err} onRetry={() => setReload((x) => x + 1)} />;
  if (!libs) return <Loading />;
  // pinned first, in the user's order, then by name
  const sorted = [...libs].sort((a, b) =>
    a.pinned !== b.pinned
      ? a.pinned
        ? -1
        : 1
      : a.pinned
        ? (a.pin_position ?? 0) - (b.pin_position ?? 0)
        : a.name.localeCompare(b.name)
  );
  return (
    <Screen title={t("big.tab.libraries")} testId="big-libraries">
      {sorted.length === 0 ? (
        <Empty>{t("big.libraries.empty")}</Empty>
      ) : (
        <Grid id="libraries" className="bs-grid--libraries">
          {sorted.map((l, i) => (
            <button
              key={l.id}
              type="button"
              role="listitem"
              className="bs-tile bs-tile--library"
              data-bs-focus={`lib:${l.id}`}
              data-bs-autofocus={i === 0 || undefined}
              onClick={() => go(bigPath.library(l.id))}
            >
              <span className="bs-tile__art">
                <Picture src={l.poster_video_ids[0] ? posterOf(l.poster_video_ids[0]) : null} />
                {l.pinned && (
                  <span className="bs-tile__pin" aria-label={t("big.pinned")}>
                    ★
                  </span>
                )}
              </span>
              <bdi className="bs-tile__title">{l.name}</bdi>
              <span className="bs-tile__meta">{t("big.videoCount", { n: n(l.videos) })}</span>
            </button>
          ))}
        </Grid>
      )}
    </Screen>
  );
}

interface VideoRowItem {
  id: string;
  title?: string | null;
  display_title?: string | null;
  filename?: string | null;
  duration_sec?: number | null;
  playback_state?: { position_sec: number; completed: boolean } | null;
}

/** A /api/videos item as a card. */
export function asCard(v: VideoRowItem): VideoCard {
  return {
    id: v.id,
    title: v.display_title || v.title,
    filename: v.filename,
    duration_sec: v.duration_sec,
    position_sec: v.playback_state?.position_sec ?? null,
    completed: v.playback_state?.completed ?? null,
  };
}

const PAGE = 48;

export function BigLibrary() {
  const { id = "" } = useParams();
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const [hub, setHub] = useState<LibraryHub | null>(null);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [videos, setVideos] = useState<VideoCard[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [reload, setReload] = useState(0);
  const gen = useRef(0);

  const page = useCallback(
    (after: string | null) => {
      const q = new URLSearchParams({
        library: id,
        sort: "created_at",
        order: "desc",
        limit: String(PAGE),
      });
      if (after) q.set("cursor", after);
      return api.get<{ items: VideoRowItem[] | null; next?: string | null }>(`/api/videos?${q}`);
    },
    [id]
  );

  useEffect(() => {
    let cancelled = false;
    const g = ++gen.current;
    setHub(null);
    setErr(null);
    setVideos([]);
    setCursor(null);
    fetchHub(id)
      .then((h) => !cancelled && setHub(h))
      .catch(
        (e) =>
          !cancelled &&
          setErr(
            e instanceof ApiError
              ? { message: errorText(e, t), status: e.status }
              : { message: errorText(e, t) }
          )
      );
    page(null)
      .then((r) => {
        if (cancelled || g !== gen.current) return;
        setVideos((r?.items ?? []).map(asCard));
        setCursor(r?.next ?? null);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [id, page, reload, t]);

  const more = () => {
    if (!cursor || loadingMore) return;
    const g = gen.current;
    setLoadingMore(true);
    page(cursor)
      .then((r) => {
        if (g !== gen.current) return;
        setVideos((cur) => [...cur, ...(r?.items ?? []).map(asCard)]);
        setCursor(r?.next ?? null);
      })
      .catch(() => undefined)
      .finally(() => setLoadingMore(false));
  };

  if (err)
    return (
      <Failed
        message={err.status === 404 ? t("library.notFound") : err.message}
        onRetry={err.status === 404 ? undefined : () => setReload((x) => x + 1)}
      />
    );
  if (!hub) return <Loading />;
  const open = (v: VideoCard) => go(bigPath.video(v.id));
  const r = hub.rows;
  return (
    <Screen
      title={<bdi>{hub.library.name}</bdi>}
      subtitle={t("big.videoCount", { n: n(hub.library.videos) })}
      testId="big-library"
    >
      <Row id="lib-ondeck" title={t("home.row.onDeck")} count={r.on_deck.length}>
        {r.on_deck.map((v) => (
          <VideoTile
            key={v.id}
            video={v}
            focusId={`lo:${v.id}`}
            meta={v.series ? <bdi>{v.series.name}</bdi> : undefined}
            onSelect={() =>
              go(
                v.series
                  ? `${bigPath.video(v.id)}?series=${encodeURIComponent(v.series.id)}`
                  : bigPath.video(v.id)
              )
            }
          />
        ))}
      </Row>
      <Row
        id="lib-continue"
        title={t("home.row.continueWatching")}
        count={r.continue_watching.length}
      >
        {r.continue_watching.map((v, i) => (
          <VideoTile
            key={v.id}
            video={v}
            focusId={`lc:${v.id}`}
            autoFocus={i === 0}
            onSelect={() => open(v)}
          />
        ))}
      </Row>
      <Row id="lib-added" title={t("home.row.recentlyAdded")} count={r.recently_added.length}>
        {r.recently_added.map((v, i) => (
          <VideoTile
            key={v.id}
            video={v}
            focusId={`la:${v.id}`}
            autoFocus={i === 0 && r.continue_watching.length === 0}
            onSelect={() => open(v)}
          />
        ))}
      </Row>
      <Row id="lib-most" title={t("big.row.mostWatched")} count={r.most_watched.length}>
        {r.most_watched.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`lm:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="lib-series" title={t("big.tab.series")} count={r.series.length}>
        {r.series.map((s) => (
          <button
            key={s.id}
            type="button"
            role="listitem"
            className="bs-tile bs-tile--text"
            data-bs-focus={`ls:${s.id}`}
            onClick={() => go(bigPath.series(s.id))}
          >
            <bdi className="bs-tile__title">{s.name || t("big.series.untitled")}</bdi>
            <span className="bs-tile__meta">{t("big.lessonCount", { n: n(s.episodes) })}</span>
          </button>
        ))}
      </Row>
      {videos.length > 0 && (
        <section className="bs-row">
          <h2 className="bs-row__title">{t("big.allVideos")}</h2>
          <Grid id="lib-all">
            {videos.map((v, i) => (
              <VideoTile
                key={v.id}
                video={v}
                focusId={`lv:${v.id}`}
                autoFocus={
                  i === 0 && r.continue_watching.length === 0 && r.recently_added.length === 0
                }
                onSelect={() => open(v)}
              />
            ))}
          </Grid>
          {cursor && (
            <div className="bs-more" data-bs-group="lib-more">
              <button
                type="button"
                className="bs-btn"
                data-bs-focus="lib:more"
                disabled={loadingMore}
                onClick={more}
              >
                {loadingMore ? t("common.loading") : t("common.loadMore")}
              </button>
            </div>
          )}
        </section>
      )}
      {hub.library.videos === 0 && <Empty>{t("big.library.empty")}</Empty>}
    </Screen>
  );
}
