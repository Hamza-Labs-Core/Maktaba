// Epic 58 story 58.08 (Epic 56 §6.4) — a collection and a playlist on the big
// screen: the art (a collage of the first four posters when there is none),
// the name, the count, the total length and the kind's badge; Play and
// Shuffle; the items in order. Choosing an item plays the list from it, on the
// server queue (story 56.01), as Play/Pause on it does.
//
//   /big/collection/:id                          curated, smart, YouTube, Plex
//   /big/library/:libraryId/collection/:kind/:id the pipeline's topic and clip groups
//   /big/playlist/:id                            the user's own, manual or smart
import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import type { VideoCard } from "../../lib/browse";
import { fetchCollection, fetchCollectionItems } from "../../lib/collections";
import { fetchAutoCollection } from "../../lib/libraryTabs";
import { fetchPlaylist } from "../../lib/me";
import { errorText } from "../../lib/errorText";
import type { QueueSourceRef } from "../../lib/playQueue";
import { usePlay } from "../play";
import { Collage, Empty, Failed, Grid, Loading, Ltr, VideoTile } from "../ui";
import { collectionBadge } from "./Library";

/** The most videos a smart list shows here (its queue plays all of them). */
const SMART_SHOWN = 200;

interface ListDoc {
  name: string;
  badge: string | null;
  items: VideoCard[];
  /** the list's own count when it has more than it shows (a smart one) */
  total?: number;
  duration?: number;
  source: QueueSourceRef;
}

/** "{h} h {m} min" of a length in seconds (collections.length). */
export function lengthText(
  sec: number,
  t: (k: string, v?: Record<string, string>) => string,
  n: (x: number) => string
) {
  const min = Math.round(sec / 60);
  return t("collections.length", { h: n(Math.floor(min / 60)), m: n(min % 60) });
}

function ListScreen({
  load,
  testId,
  notFound,
}: {
  load: () => Promise<ListDoc>;
  testId: string;
  notFound: string;
}) {
  const { t, n } = useI18n();
  const play = usePlay();
  const [doc, setDoc] = useState<ListDoc | null>(null);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let cancelled = false;
    setErr(null);
    setDoc(null);
    load()
      .then((d) => !cancelled && setDoc(d))
      .catch(
        (e) =>
          !cancelled &&
          setErr(
            e instanceof ApiError
              ? { message: errorText(e, t), status: e.status }
              : { message: errorText(e, t) }
          )
      );
    return () => {
      cancelled = true;
    };
    // the loader is the route's; it changes with the route's ids
  }, [testId, reload, t]);
  if (err)
    return (
      <Failed
        message={err.status === 404 ? notFound : err.message}
        onRetry={err.status === 404 ? undefined : () => setReload((x) => x + 1)}
      />
    );
  if (!doc) return <Loading />;
  const count = doc.total ?? doc.items.length;
  const length = doc.duration ?? doc.items.reduce((sum, v) => sum + (v.duration_sec ?? 0), 0);
  const from = (id?: string) => void play(doc.source, { startVideoId: id });
  return (
    <section className="bs-screen" data-testid={testId}>
      <section className="bs-hero bs-hero--list">
        <Collage videoIds={doc.items.map((v) => v.id)} className="bs-hero__art" />
        <div className="bs-hero__text">
          {doc.badge && <p className="bs-hero__kicker">{doc.badge}</p>}
          <h1 className="bs-hero__title">
            <bdi>{doc.name}</bdi>
          </h1>
          <p className="bs-hero__meta">
            {t("collections.count", { n: n(count) })}
            {length > 0 && (
              <>
                {" · "}
                <Ltr>{lengthText(length, t, n)}</Ltr>
              </>
            )}
          </p>
          <div className="bs-hero__actions" data-bs-group="list-actions">
            <button
              type="button"
              className="bs-btn bs-btn--primary"
              data-bs-focus="list:play"
              data-bs-autofocus
              disabled={count === 0}
              onClick={() => from()}
            >
              ▶ {t("big.play")}
            </button>
            <button
              type="button"
              className="bs-btn"
              data-bs-focus="list:shuffle"
              disabled={count < 2}
              onClick={() => void play(doc.source, { shuffle: true })}
            >
              ⤮ {t("big.shuffle")}
            </button>
          </div>
        </div>
      </section>
      {doc.items.length === 0 ? (
        <Empty>{t("big.list.empty")}</Empty>
      ) : (
        <Grid id="list-items">
          {doc.items.map((v) => (
            <VideoTile
              key={v.id}
              video={v}
              focusId={`li:${v.id}`}
              onSelect={() => from(v.id)}
              onPlay={() => from(v.id)}
            />
          ))}
        </Grid>
      )}
    </section>
  );
}

function asCard(v: {
  id: string;
  title?: string | null;
  display_title?: string | null;
  filename?: string | null;
  duration_sec?: number | null;
  playback_state?: { position_sec: number; completed: boolean } | null;
}): VideoCard {
  return {
    id: v.id,
    title: v.display_title || v.title,
    filename: v.filename,
    duration_sec: v.duration_sec,
    position_sec: v.playback_state?.position_sec ?? null,
    completed: v.playback_state?.completed ?? null,
  };
}

export function BigCollection() {
  const { id = "" } = useParams();
  const { t } = useI18n();
  return (
    <ListScreen
      testId={`big-collection:${id}`}
      notFound={t("collections.notFound")}
      load={async () => {
        const col = await fetchCollection(id);
        let items: VideoCard[];
        let total: number | undefined;
        if (col.is_smart) {
          // a saved filter: its library's videos with those filters
          const q = new URLSearchParams({
            library: col.library_id,
            ...(col.filters ?? {}),
            limit: String(SMART_SHOWN),
          });
          const r = await api.get<{ items?: Parameters<typeof asCard>[0][] | null }>(
            `/api/videos?${q}`
          );
          items = (Array.isArray(r?.items) ? r.items : []).map(asCard);
        } else {
          items = await fetchCollectionItems(id);
          total = col.videos ?? undefined;
        }
        return {
          name: col.name_override || col.name,
          badge: collectionBadge(
            {
              kind: col.is_smart ? "smart" : col.source_kind === "plex" ? "plex" : "curated",
              source_kind: col.source_kind,
            },
            t
          ),
          items,
          total,
          source: { kind: "collection", id: col.id },
        };
      }}
    />
  );
}

export function BigAutoCollection() {
  const { libraryId = "", kind = "", id = "" } = useParams();
  const { t } = useI18n();
  return (
    <ListScreen
      testId={`big-collection:${kind}:${id}`}
      notFound={t("collections.notFound")}
      load={async () => {
        const items: VideoCard[] = [];
        let offset = 0;
        let page = await fetchAutoCollection(libraryId, kind, id);
        const name = page.collection.name;
        const total = page.collection.videos;
        items.push(...page.items);
        // the first pages; the queue plays the whole group
        for (let i = 0; i < 4 && page.next !== null; i++) {
          offset = page.next;
          page = await fetchAutoCollection(libraryId, kind, id, offset);
          items.push(...page.items);
        }
        return {
          name,
          badge: null,
          items,
          total,
          source: { kind: "collection", id: `${kind === "clip" ? "clip" : "topic"}:${id}` },
        };
      }}
    />
  );
}

export function BigPlaylist() {
  const { id = "" } = useParams();
  const { t } = useI18n();
  return (
    <ListScreen
      testId={`big-playlist:${id}`}
      notFound={t("playlists.notFound")}
      load={async () => {
        const d = await fetchPlaylist(id);
        return {
          name: d.playlist.name,
          badge: d.playlist.kind === "smart" ? t("collections.badge.smart") : null,
          items: d.items,
          total: d.playlist.videos,
          duration: d.playlist.duration_sec || undefined,
          source: { kind: "playlist", id },
        };
      }}
    />
  );
}
