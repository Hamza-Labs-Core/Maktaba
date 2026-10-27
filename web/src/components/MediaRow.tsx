// Epic 45 — a row of cards that scrolls sideways (Home, a library's
// Overview), Plex-style: a title (a link when the row has its own page), an
// optional "See all", and back / forward buttons. Under dir="rtl" the row
// starts on the right, so "forward" scrolls left and the arrows swap. The
// cards are links (Tab walks them; the row scrolls to keep focus in view,
// with scroll-padding-inline so the focused card is not under the edge).
import { Children, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import { watchedFraction, type VideoCard } from "../lib/browse";
import { VideoPoster, formatDuration } from "./VideoViews";

interface MediaRowProps {
  title: ReactNode;
  /** the title links here (a pinned library's page) */
  titleTo?: string;
  /** a "See all" link */
  seeAllTo?: string;
  /** the row's <li> items; a row without any renders nothing */
  children: ReactNode;
  className?: string;
  testId?: string;
}

export function MediaRow({ title, titleTo, seeAllTo, children, className, testId }: MediaRowProps) {
  const { t, dir } = useI18n();
  const headingId = useId();
  const scroller = useRef<HTMLUListElement | null>(null);
  // false only when the row is measured and fits: no buttons then
  const [overflows, setOverflows] = useState(true);
  const count = Children.toArray(children).length;

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const measure = () => setOverflows(el.clientWidth === 0 || el.scrollWidth > el.clientWidth + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [count]);

  if (count === 0) return null;

  // a page of cards at a time; forward is toward the row's end (left under RTL)
  function page(forward: boolean) {
    const el = scroller.current;
    if (!el) return;
    const step = Math.max(el.clientWidth * 0.9, 200);
    const sign = (forward ? 1 : -1) * (dir === "rtl" ? -1 : 1);
    el.scrollBy?.({ left: sign * step, behavior: "smooth" });
  }

  const rtl = dir === "rtl";
  return (
    <section
      className={`mkt-row${className ? ` ${className}` : ""}`}
      aria-labelledby={headingId}
      data-testid={testId}
    >
      <header className="mkt-row__head">
        <h2 id={headingId} className="mkt-row__title">
          {titleTo ? <Link to={titleTo}>{title}</Link> : title}
        </h2>
        {seeAllTo && (
          <Link className="mkt-row__seeall" to={seeAllTo}>
            {t("home.seeAll")}
          </Link>
        )}
        {overflows && (
          <span className="mkt-row__nav">
            <button
              type="button"
              className="mkt-row__btn"
              aria-label={t("home.scrollBack")}
              onClick={() => page(false)}
            >
              <span aria-hidden="true">{rtl ? "›" : "‹"}</span>
            </button>
            <button
              type="button"
              className="mkt-row__btn"
              aria-label={t("home.scrollForward")}
              onClick={() => page(true)}
            >
              <span aria-hidden="true">{rtl ? "‹" : "›"}</span>
            </button>
          </span>
        )}
      </header>
      <ul className="mkt-row__scroller" ref={scroller}>
        {children}
      </ul>
    </section>
  );
}

/** A video's card in a row: poster, how far you got, title and length. */
export function MediaCard({ video, meta }: { video: VideoCard; meta?: ReactNode }) {
  const { t, n } = useI18n();
  const frac = watchedFraction(video);
  const name = video.title || video.filename || video.id;
  return (
    <li className="mkt-row__item">
      <Link to={`/videos/${encodeURIComponent(video.id)}`} className="mkt-media-card">
        <span className="mkt-media-card__art">
          <VideoPoster videoId={video.id} />
          {typeof video.duration_sec === "number" && video.duration_sec > 0 && (
            <span className="mkt-media-card__dur">{formatDuration(video.duration_sec)}</span>
          )}
          {video.completed && (
            <span className="mkt-card__watched" role="img" aria-label={t("browse.watchedMark")}>
              ✓
            </span>
          )}
          {frac !== null && (
            <span
              className="mkt-media-card__progress"
              role="progressbar"
              aria-label={t("home.watched")}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={Math.round(frac * 100)}
              aria-valuetext={t("home.watchedPct", { pct: n(Math.round(frac * 100)) })}
            >
              <span style={{ inlineSize: `${frac * 100}%` }} />
            </span>
          )}
        </span>
        <bdi className="mkt-media-card__title">{name}</bdi>
        {meta && <span className="mkt-media-card__meta">{meta}</span>}
      </Link>
    </li>
  );
}

/** A row of video cards; hidden when it has none. */
export function VideoRow({
  title,
  titleTo,
  seeAllTo,
  videos,
  testId,
  meta,
}: {
  title: ReactNode;
  titleTo?: string;
  seeAllTo?: string;
  videos: VideoCard[];
  testId?: string;
  /** a line under each card's title (On Deck: the series) */
  meta?: (v: VideoCard) => ReactNode;
}) {
  return (
    <MediaRow title={title} titleTo={titleTo} seeAllTo={seeAllTo} testId={testId}>
      {videos.map((v) => (
        <MediaCard key={v.id} video={v} meta={meta?.(v)} />
      ))}
    </MediaRow>
  );
}
