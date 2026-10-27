// Epic 46 — the big-screen building blocks: a screen's frame, rows that
// scroll sideways, grids, poster tiles, the dialog, and the states (loading,
// error, empty). Every control is a real <button> carrying data-bs-focus (a
// stable id, for Back's focus restore) inside a data-bs-group (its row or
// grid), so the focus engine, the browser's Tab and a screen reader agree.
import { useRef, useState, type ReactNode } from "react";
import { useI18n } from "../lib/i18n";
import { watchedFraction, type VideoCard } from "../lib/browse";
import { formatDuration } from "../components/VideoViews";
import { useLayer } from "./focus/FocusProvider";

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

export function cardTitle(v: { title?: string | null; filename?: string | null; id: string }) {
  return v.title || v.filename || v.id;
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

/** A video's poster tile in a row or a grid. */
export function VideoTile({
  video,
  focusId,
  onSelect,
  autoFocus,
  meta,
}: {
  video: VideoCard;
  focusId: string;
  onSelect: () => void;
  autoFocus?: boolean;
  meta?: ReactNode;
}) {
  const { t } = useI18n();
  const frac = watchedFraction(video);
  const name = cardTitle(video);
  return (
    <button
      type="button"
      role="listitem"
      className="bs-tile"
      data-bs-focus={focusId}
      data-bs-autofocus={autoFocus || undefined}
      onClick={onSelect}
    >
      <span className="bs-tile__art">
        <Picture src={posterOf(video.id)} />
        {typeof video.duration_sec === "number" && video.duration_sec > 0 && (
          <span className="bs-tile__dur">{formatDuration(video.duration_sec)}</span>
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
