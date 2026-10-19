// A mock of the channel strip (Story 27.11) over a 16:9 sample frame: the
// bar the streaming engine burns into the picture, with the channel's real
// now / next lines, the clock box at the strip's end (left in a right-to-left
// strip, right otherwise) rotating through the cities, 12/24 h and
// Arabic-Indic or Western digits.
import { useEffect, useState, type CSSProperties } from "react";
import type { StripSettings } from "../../../lib/channels";
import {
  arabicDigits,
  clockIndex,
  clockLabel,
  formatClock,
  nextLabel,
  stripDirection,
} from "../../../lib/strip";

export interface StripPreviewProps {
  settings: StripSettings;
  /** what is on now (its on-air line) and next */
  now: string;
  next?: string | null;
  channelName?: string;
  /** the sample frame (the channel's loading picture); none: a neutral gradient */
  image?: string | null;
  /** for tests: the clock */
  clock?: () => Date;
}

const systemClock = () => new Date();

function rgba(hex: string, alpha: number): string {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
  if (!m) return hex;
  const [r, g, b] = [m[1], m[2], m[3]].map((h) => parseInt(h, 16));
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

export function StripPreview({
  settings: s,
  now,
  next,
  channelName,
  image,
  clock = systemClock,
}: StripPreviewProps) {
  const [at, setAt] = useState(() => clock());
  useEffect(() => {
    const id = setInterval(() => setAt(clock()), 1000);
    return () => clearInterval(id);
  }, [clock]);

  const dir = stripDirection(s, [now, next]);
  const indic = arabicDigits(s, dir);
  const i = s.show_clock ? clockIndex(s.clocks.length, s.clock_seconds, at.getTime()) : -1;
  const city = i >= 0 ? s.clocks[i] : null;
  const showNext = s.show_next && Boolean(next);

  const bar: CSSProperties = {
    height: `${s.height_pct * (showNext ? 1.45 : 1)}%`,
    background: rgba(s.bg_color, s.bg_opacity),
    color: s.text_color,
    [s.position === "top" ? "top" : "bottom"]: 0,
  };
  // the title is font_size_pct of the strip, the strip height_pct of the frame
  const titleSize = `${(s.height_pct * s.font_size_pct) / 100}cqh`;

  return (
    <div
      className="mkt-strip-preview"
      data-testid="strip-preview"
      style={image ? { backgroundImage: `url(${image})` } : undefined}
    >
      <div
        className="mkt-strip-preview__bar"
        data-testid="strip-bar"
        data-position={s.position}
        data-clock-side={dir === "rtl" ? "left" : "right"}
        dir={dir}
        style={bar}
      >
        <div className="mkt-strip-preview__text" dir={dir}>
          {s.show_channel_name && channelName && (
            <span className="mkt-strip-preview__channel">{channelName}</span>
          )}
          <span
            className={`mkt-strip-preview__now${
              s.overflow === "scroll" ? " mkt-strip-preview__now--scroll" : ""
            }`}
            data-testid="strip-now"
            style={{ fontSize: titleSize }}
          >
            <span>{now}</span>
          </span>
          {showNext && (
            <span className="mkt-strip-preview__next" data-testid="strip-next">
              {nextLabel(s, dir)}: {next}
            </span>
          )}
        </div>
        {city && (
          <div
            className="mkt-strip-preview__clock"
            data-testid="strip-clock"
            style={{ background: s.accent_color }}
          >
            <span className="mkt-strip-preview__city">{clockLabel(city, dir)}</span>
            <span className="mkt-strip-preview__time" dir="ltr">
              {formatClock(at, city.zone, s.clock_format, indic)}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
