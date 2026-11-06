// Epic 46 — big screen's Home: a hero for what to continue (Resume, Details),
// then rows: Continue Watching, Recently Added, Recently Watched, Live TV on
// now, one per pinned library. GET /api/home and GET /api/channels/now, the
// same reads as the desk Home; empty rows are hidden.
import { useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { fetchHome, watchedFraction, type HomeResponse, type VideoCard } from "../../lib/browse";
import { channelsApi, type NowEntry } from "../../lib/channels";
import { bigPath, useBigNav } from "../nav";
import {
  Empty,
  Failed,
  Loading,
  Picture,
  Progress,
  Row,
  Screen,
  VideoTile,
  cardTitle,
  clock,
  posterOf,
} from "../ui";
import { ChannelTile } from "./Live";
import { errorText } from "../../lib/errorText";
import { useFeature } from "../../lib/capabilities";
import { currentItem, fetchCurrentQueue, resumeQueue, type ServerQueue } from "../../lib/playQueue";
import { CONTINUE_QUEUE_WITHIN_MS, deviceLabel } from "../../components/ContinueQueue";

/** Epic 56 story 56.13: the queue last used on any device in the last day, to continue. */
function BigContinueQueue() {
  const { t } = useI18n();
  const { go } = useBigNav();
  const [q, setQ] = useState<ServerQueue | null>(null);
  useEffect(() => {
    let live = true;
    fetchCurrentQueue()
      .then((x) => {
        const used = x?.used_at ? Date.parse(x.used_at) : NaN;
        if (live && x && Number.isFinite(used) && Date.now() - used <= CONTINUE_QUEUE_WITHIN_MS) setQ(x);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);
  const cur = q ? currentItem(q) : null;
  if (!q || !cur) return null;
  const title = cur.title || cur.filename || "";
  const device = deviceLabel(q.device_id, t);
  return (
    <div className="bs-hero__actions" data-bs-group="continue-queue" data-testid="big-continue-queue">
      <button
        type="button"
        className="bs-btn bs-btn--primary"
        data-bs-focus="continue-queue"
        onClick={() => {
          const id = resumeQueue(q);
          const at = Math.floor(cur.position_sec ?? 0);
          if (id) go(bigPath.watch(id, at > 0 ? { t: at } : {}));
        }}
      >
        ▶ {t("home.continueQueue")}:{" "}
        <bdi>{device ? t("home.continueQueue.from", { title, device }) : title}</bdi>
      </button>
    </div>
  );
}

export function BigHome() {
  const { t } = useI18n();
  const { go } = useBigNav();
  const [home, setHome] = useState<HomeResponse | null>(null);
  const [channels, setLive] = useState<NowEntry[]>([]);
  // no channel row while Live TV is off on this home (story 51.11)
  const live = useFeature("live_tv") === "on" ? channels : [];
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    fetchHome()
      .then((h) => !cancelled && setHome(h))
      .catch((e) => !cancelled && setErr(errorText(e, t)));
    channelsApi
      .now()
      .then((r) => {
        if (cancelled) return;
        const items = Array.isArray(r?.items)
          ? r.items.filter((e) => e && e.channel && typeof e.channel.number === "number")
          : [];
        setLive(items.sort((a, b) => a.channel.number - b.channel.number));
      })
      .catch(() => !cancelled && setLive([]));
    return () => {
      cancelled = true;
    };
  }, [reload, t]);

  if (err) return <Failed message={err} onRetry={() => setReload((x) => x + 1)} />;
  if (!home) return <Loading />;

  const hero = home.continue_watching[0] ?? home.on_deck[0] ?? home.recently_added[0] ?? null;
  const resuming = !!home.continue_watching[0];
  // an On Deck lesson opens with its series, so the player knows the next one
  const openLesson = (v: VideoCard) =>
    go(
      v.series
        ? `${bigPath.video(v.id)}?series=${encodeURIComponent(v.series.id)}`
        : bigPath.video(v.id)
    );
  const open = (v: VideoCard) => go(bigPath.video(v.id));
  const pinned = home.pinned.filter(
    (p) => p.continue_watching.length > 0 || p.recently_added.length > 0
  );
  const empty =
    !hero &&
    home.recently_watched.length === 0 &&
    home.favorites.length === 0 &&
    pinned.length === 0 &&
    live.length === 0;

  return (
    <Screen testId="big-home">
      {hero && <Hero video={hero} resuming={resuming} />}
      <BigContinueQueue />
      <Row
        id="continue"
        title={t("home.row.continueWatching")}
        count={home.continue_watching.length}
      >
        {home.continue_watching.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`continue:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="ondeck" title={t("home.row.onDeck")} count={home.on_deck.length}>
        {home.on_deck.map((v) => (
          <VideoTile
            key={v.id}
            video={v}
            focusId={`ondeck:${v.id}`}
            meta={v.series ? <bdi>{v.series.name}</bdi> : undefined}
            onSelect={() => openLesson(v)}
          />
        ))}
      </Row>
      <Row id="added" title={t("home.row.recentlyAdded")} count={home.recently_added.length}>
        {home.recently_added.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`added:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="watched" title={t("home.row.recentlyWatched")} count={home.recently_watched.length}>
        {home.recently_watched.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`watched:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="favorites" title={t("home.row.favorites")} count={home.favorites.length}>
        {home.favorites.map((v) => (
          <VideoTile key={v.id} video={v} focusId={`fav:${v.id}`} onSelect={() => open(v)} />
        ))}
      </Row>
      <Row id="live" title={t("won.title")} count={live.length}>
        {live.map((e) => (
          <ChannelTile key={e.channel.id} entry={e} focusId={`live:${e.channel.id}`} />
        ))}
      </Row>
      {pinned.map((p) => {
        const seen = new Set<string>();
        const videos = [...p.continue_watching, ...p.recently_added].filter((v) =>
          seen.has(v.id) ? false : (seen.add(v.id), true)
        );
        return (
          <Row
            key={p.library.id}
            id={`pin:${p.library.id}`}
            title={<bdi>{p.library.name}</bdi>}
            count={videos.length}
          >
            {videos.map((v) => (
              <VideoTile
                key={v.id}
                video={v}
                focusId={`pin:${p.library.id}:${v.id}`}
                onSelect={() => open(v)}
              />
            ))}
            <button
              type="button"
              role="listitem"
              className="bs-tile bs-tile--more"
              data-bs-focus={`pin:${p.library.id}:more`}
              onClick={() => go(bigPath.library(p.library.id))}
            >
              <span className="bs-tile__art bs-tile__art--more">›</span>
              <span className="bs-tile__title">{t("big.openLibrary")}</span>
            </button>
          </Row>
        );
      })}
      {empty && (
        <Empty>
          <h2>{t("home.empty.title")}</h2>
          <p>{t("big.home.empty")}</p>
          <button
            type="button"
            className="bs-btn bs-btn--primary"
            data-bs-focus="home:libraries"
            data-bs-autofocus
            onClick={() => go(bigPath.libraries)}
          >
            {t("home.empty.libraries")}
          </button>
        </Empty>
      )}
    </Screen>
  );
}

function Hero({ video, resuming }: { video: VideoCard; resuming: boolean }) {
  const { t } = useI18n();
  const { go } = useBigNav();
  const frac = watchedFraction(video);
  const pos = video.position_sec ?? 0;
  return (
    <section className="bs-hero" data-testid="big-hero">
      <Picture src={posterOf(video.id)} className="bs-hero__art" />
      <div className="bs-hero__text">
        <p className="bs-hero__kicker">{resuming ? t("big.hero.continue") : t("big.hero.new")}</p>
        <h1 className="bs-hero__title">
          <bdi>{cardTitle(video)}</bdi>
        </h1>
        {resuming && frac !== null && (
          <p className="bs-hero__meta">
            <Progress fraction={frac} label={t("home.watched")} />
            <span>
              {clock(pos)}
              {video.duration_sec ? ` / ${clock(video.duration_sec)}` : ""}
            </span>
          </p>
        )}
        <div className="bs-hero__actions" data-bs-group="hero">
          <button
            type="button"
            className="bs-btn bs-btn--primary"
            data-bs-focus="hero:play"
            data-bs-autofocus
            onClick={() => go(bigPath.watch(video.id, { t: resuming ? pos : 0 }))}
          >
            ▶ {resuming ? t("big.resume") : t("video.play")}
          </button>
          <button
            type="button"
            className="bs-btn"
            data-bs-focus="hero:details"
            onClick={() => go(bigPath.video(video.id))}
          >
            {t("big.details")}
          </button>
        </div>
      </div>
    </section>
  );
}
