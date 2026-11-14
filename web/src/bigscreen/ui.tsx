// Epic 46 — the big-screen building blocks: a screen's frame, rows that
// scroll sideways, grids, poster tiles, the dialog, and the states (loading,
// error, empty). Every control is a real <button> carrying data-bs-focus (a
// stable id, for Back's focus restore) inside a data-bs-group (its row or
// grid), so the focus engine, the browser's Tab and a screen reader agree.
import { useRef, useState, type ReactNode } from "react";
import { useI18n } from "../lib/i18n";
import { videoTitle } from "../lib/videoTitle";
import { watchedFraction, type VideoCard } from "../lib/browse";
import { formatDuration } from "../components/VideoViews";
import { useLayer } from "./focus/FocusProvider";
import { cardRef, type CardActions } from "./cards";
import { usePlay } from "./play";

export function Screen({
  title,
  subtitle,
  children,
  testId,
}: {
  title?: ReactNode;
  subtitle?: ReactNode;
  children: ReactNode;
  testId?: string;
}) {
  return (
    <section className="bs-screen" data-testid={testId}>
      {title && (
        <header className="bs-screen__head">
          <h1 className="bs-screen__title">{title}</h1>
          {subtitle && <p className="bs-screen__sub">{subtitle}</p>}
        </header>
      )}
      {children}
    </section>
  );
}

export function Row({
  id,
  title,
  children,
  count,
}: {
  id: string;
  title: ReactNode;
  children: ReactNode;
  /** the row's item count: an empty row renders nothing */
  count: number;
}) {
  if (count === 0) return null;
  return (
    <section className="bs-row" aria-label={typeof title === "string" ? title : undefined}>
      <h2 className="bs-row__title">{title}</h2>
      <div className="bs-row__scroller" data-bs-group={`row:${id}`} role="list">
        {children}
      </div>
    </section>
  );
}

export function Grid({
  id,
  children,
  className,
}: {
  id: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={`bs-grid${className ? ` ${className}` : ""}`}
      data-bs-group={`grid:${id}`}
      role="list"
    >
      {children}
    </div>
  );
}

/** A left-to-right run (a time, a code) inside a sentence: marked with LRM on each side. */
export function lrm(text: string): string {
  return `\u200E${text}\u200E`;
}

/** A picture that leaves a plain panel when it does not load (a video without a thumbnail yet). */
export function Picture({ src, className }: { src: string | null; className?: string }) {
  const [broken, setBroken] = useState(false);
  if (!src || broken)
    return <span className={`bs-pic bs-pic--none ${className ?? ""}`} aria-hidden />;
  return (
    <img
      className={`bs-pic ${className ?? ""}`}
      src={src}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  );
}

export const posterOf = (videoId: string) => `/api/videos/${encodeURIComponent(videoId)}/poster`;
/** A card's poster: the small size (story 48.5), what a 10-foot grid needs. */
export const posterSmall = (videoId: string) => `${posterOf(videoId)}?size=small`;

export function cardTitle(v: { title?: string | null; filename?: string | null; id: string }) {
  return videoTitle(v);
}

export function Progress({ fraction, label }: { fraction: number; label: string }) {
  const pct = Math.round(Math.min(1, Math.max(0, fraction)) * 100);
  return (
    <span
      className="bs-progress"
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={pct}
    >
      <span style={{ inlineSize: `${pct}%` }} />
    </span>
  );
}

/** A video's poster tile in a row or a grid. Play/Pause plays it; a long OK opens its actions. */
export function VideoTile({
  video,
  focusId,
  onSelect,
  autoFocus,
  meta,
  onPlay,
}: {
  video: VideoCard;
  focusId: string;
  onSelect: () => void;
  autoFocus?: boolean;
  meta?: ReactNode;
  /** Play/Pause on the card, when not the video on its own (a collection plays from it) */
  onPlay?: () => void;
}) {
  const { t } = useI18n();
  const play = usePlay();
  const frac = watchedFraction(video);
  const name = cardTitle(video);
  const series = video.series?.id ?? null;
  const actions: CardActions = {
    kind: "video",
    title: name,
    videoId: video.id,
    seriesId: series,
    watched: !!video.completed,
    play:
      onPlay ??
      (() =>
        void play(
          { kind: "video", id: video.id },
          { at: video.completed ? 0 : video.position_sec, series }
        )),
  };
  return (
    <button
      ref={cardRef(actions)}
      type="button"
      role="listitem"
      className="bs-tile"
      data-bs-focus={focusId}
      data-bs-autofocus={autoFocus || undefined}
      onClick={onSelect}
    >
      <span className="bs-tile__art">
        <Picture src={posterSmall(video.id)} />
        {typeof video.duration_sec === "number" && video.duration_sec > 0 && (
          <Ltr className="bs-tile__dur">{formatDuration(video.duration_sec)}</Ltr>
        )}
        {video.completed && (
          <span className="bs-tile__done" aria-label={t("big.watched")}>
            ✓
          </span>
        )}
        {frac !== null && <Progress fraction={frac} label={t("home.watched")} />}
      </span>
      <bdi className="bs-tile__title">{name}</bdi>
      {meta && <span className="bs-tile__meta">{meta}</span>}
    </button>
  );
}

/**
 * Text that reads left to right inside either direction: a time (1:02:03), an
 * episode code (S01E02), a resolution (1080p). Isolated, so in Arabic it does
 * not reorder the words around it, nor they it.
 */
export function Ltr({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <bdi dir="ltr" className={className}>
      {children}
    </bdi>
  );
}

/** Up to four posters as one picture (a collection or playlist with no art of its own). */
export function Collage({
  videoIds,
  className,
}: {
  videoIds: readonly string[];
  className?: string;
}) {
  const ids = videoIds.slice(0, 4);
  if (ids.length <= 1)
    return <Picture src={ids[0] ? posterSmall(ids[0]) : null} className={className} />;
  return (
    <span className={`bs-collage bs-collage--${ids.length} ${className ?? ""}`} aria-hidden>
      {ids.map((id) => (
        <Picture key={id} src={posterSmall(id)} />
      ))}
    </span>
  );
}

/** A card that is not one video: a series, a collection, a playlist. */
export function CardTile({
  focusId,
  title,
  meta,
  badge,
  art,
  actions,
  onSelect,
  autoFocus,
}: {
  focusId: string;
  title: string;
  meta?: ReactNode;
  badge?: string | null;
  art: ReactNode;
  actions: CardActions | null;
  onSelect: () => void;
  autoFocus?: boolean;
}) {
  return (
    <button
      ref={cardRef(actions)}
      type="button"
      role="listitem"
      className="bs-tile"
      data-bs-focus={focusId}
      data-bs-autofocus={autoFocus || undefined}
      onClick={onSelect}
    >
      <span className="bs-tile__art">
        {art}
        {badge && <span className="bs-tile__badge">{badge}</span>}
      </span>
      <bdi className="bs-tile__title">{title}</bdi>
      {meta && <span className="bs-tile__meta">{meta}</span>}
    </button>
  );
}

export function Loading() {
  const { t } = useI18n();
  return (
    <p className="bs-state" aria-busy="true">
      {t("common.loading")}
    </p>
  );
}

export function Failed({ message, onRetry }: { message: string; onRetry?: () => void }) {
  const { t } = useI18n();
  return (
    <div className="bs-state bs-state--error" role="alert">
      <p>{message}</p>
      {onRetry && (
        <button
          type="button"
          className="bs-btn"
          data-bs-focus="retry"
          data-bs-autofocus
          onClick={onRetry}
        >
          {t("common.retry")}
        </button>
      )}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="bs-state">{children}</div>;
}

/** A modal dialog: its first control takes focus; Back (or a choice) closes it. */
export function Dialog({
  title,
  body,
  children,
  onClose,
  testId,
}: {
  title: ReactNode;
  body?: ReactNode;
  children: ReactNode;
  onClose: () => void;
  testId?: string;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  useLayer(ref, onClose);
  return (
    <div className="bs-dialog__scrim">
      <div
        ref={ref}
        className="bs-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="bs-dialog-title"
        data-bs-layer
        data-testid={testId}
      >
        <h2 id="bs-dialog-title" className="bs-dialog__title">
          {title}
        </h2>
        {body && <p className="bs-dialog__body">{body}</p>}
        <div className="bs-dialog__actions" data-bs-group="dialog">
          {children}
        </div>
      </div>
    </div>
  );
}

/** A side panel (the player's Chapters / Subtitles / Audio): a layer like a dialog. */
export function Panel({
  title,
  children,
  onClose,
  testId,
}: {
  title: ReactNode;
  children: ReactNode;
  onClose: () => void;
  testId?: string;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  useLayer(ref, onClose);
  return (
    <aside
      ref={ref}
      className="bs-panel"
      role="dialog"
      aria-modal="true"
      aria-label={typeof title === "string" ? title : undefined}
      data-bs-layer
      data-testid={testId}
    >
      <h2 className="bs-panel__title">{title}</h2>
      <div className="bs-panel__list" data-bs-group="panel">
        {children}
      </div>
    </aside>
  );
}

/** h:mm:ss / m:ss of a time in seconds. */
export function clock(sec: number): string {
  return formatDuration(Math.max(0, Math.floor(sec)));
}
