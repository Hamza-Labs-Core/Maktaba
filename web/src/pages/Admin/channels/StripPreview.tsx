// A mock of the channel strip (Story 27.11) over a 16:9 sample frame: the
// bar the streaming engine burns into the picture, with the channel's real
// now / next lines, the clock box at the strip's end (left in a right-to-left
// strip, right otherwise) rotating through the cities, 12/24 h and
// Arabic-Indic or Western digits. With overflow "cycle" a long line shows in
// pages that take turns, laid out as the engine lays them out: in the
// output picture's pixels (1920×1080), measured in the strip's font.
import { useEffect, useMemo, useState, type CSSProperties } from "react";
import type { StripSettings } from "../../../lib/channels";
import {
  NEXT_PAGE_FACTOR,
  arabicDigits,
  clockIndex,
  clockLabel,
  formatClock,
  nextLabel,
  pageIndicator,
  pageLine,
  pageSeconds,
  ruleThickness,
  estimateWidth,
  stripDirection,
  stripLayout,
  type Measure,
} from "../../../lib/strip";

export interface StripPreviewProps {
  settings: StripSettings;
  /** what is on now (its on-air line) and next */
  now: string;
  next?: string | null;
  /** the reference of a verse the current title quotes: the last page */
  nowRef?: string | null;
  channelName?: string;
  /** the sample frame (the channel's loading picture); none: a neutral gradient */
  image?: string | null;
  /** for tests: the clock */
  clock?: () => Date;
  /** for tests: text width in output pixels at a font size */
  measure?: (text: string, fontPx: number) => number;
}

/** The output picture the engine draws on (the preview is that, scaled). */
export const FRAME_W = 1920;
export const FRAME_H = 1080;
const FONT = '"Noto Naskh Arabic", "Noto Sans Arabic", Arial, Tahoma, sans-serif';

const systemClock = () => new Date();

let canvas: CanvasRenderingContext2D | null | undefined;
/** Canvas measureText in the strip's font; the engine's per-character estimate without one. */
export function canvasMeasure(text: string, fontPx: number): number {
  if (canvas === undefined) {
    // jsdom has no canvas (and says so): a browser always has one
    const ua = typeof navigator !== "undefined" ? navigator.userAgent : "";
    canvas =
      typeof document !== "undefined" && !ua.includes("jsdom")
        ? (document.createElement("canvas").getContext("2d") ?? null)
        : null;
  }
  if (canvas) {
    canvas.font = `${fontPx}px ${FONT}`;
    const w = canvas.measureText(text).width;
    if (w > 0) return w;
  }
  return estimateWidth(text, fontPx);
}

function rgba(hex: string, alpha: number): string {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
  if (!m) return hex;
  const [r, g, b] = [m[1], m[2], m[3]].map((h) => parseInt(h, 16));
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

/** Cycles through the pages, each for its own time (from page 1 when they change). */
function usePage(pages: string[], seconds: number[]): number {
  const [state, setState] = useState({ key: "", i: 0 });
  const key = pages.join("\u0000");
  const i = state.key === key ? state.i : 0;
  const n = pages.length;
  const wait = seconds[i] ?? 0;
  useEffect(() => {
    if (n < 2) return;
    const id = setTimeout(() => setState({ key, i: (i + 1) % n }), wait * 1000);
    return () => clearTimeout(id);
  }, [key, i, n, wait]);
  return Math.min(i, Math.max(0, n - 1));
}

export function StripPreview({
  settings: s,
  now,
  next,
  nowRef,
  channelName,
  image,
  clock = systemClock,
  measure = canvasMeasure,
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
  const nextText = showNext ? `${nextLabel(s, dir)}: ${next}` : "";
  const cycle = s.overflow === "cycle";

  const geo = stripLayout(s, {
    W: FRAME_W,
    H: FRAME_H,
    clock: s.show_clock && s.clocks.length > 0,
    hasNext: showNext,
    channelName,
    measure,
  });
  const nowPaging = useMemo(() => {
    const m: Measure = (t) => measure(t, geo.titlePx);
    const pages = cycle
      ? pageLine(now, geo.areaWidth, m, nowRef)
      : [now + (nowRef ? ` · ${nowRef}` : "")];
    return { pages, secs: pages.map((p) => pageSeconds(m(p), geo.areaWidth, s.page_seconds)) };
  }, [cycle, now, nowRef, geo.areaWidth, geo.titlePx, measure, s.page_seconds]);
  const nextPaging = useMemo(() => {
    const m: Measure = (t) => measure(t, geo.nextPx);
    const pages = !nextText ? [] : cycle ? pageLine(nextText, geo.areaWidth, m) : [nextText];
    return {
      pages,
      secs: pages.map((p) => NEXT_PAGE_FACTOR * pageSeconds(m(p), geo.areaWidth, s.page_seconds)),
    };
  }, [cycle, nextText, geo.areaWidth, geo.nextPx, measure, s.page_seconds]);
  const ni = usePage(nowPaging.pages, nowPaging.secs);
  const xi = usePage(nextPaging.pages, nextPaging.secs);

  const bar: CSSProperties = {
    height: `${(geo.h / FRAME_H) * 100}%`,
    background: rgba(s.bg_color, s.bg_opacity),
    color: s.text_color,
    [s.position === "top" ? "top" : "bottom"]: 0,
  };
  // the accent rule along the edge facing the picture: the top of a bottom bar
  const rule = ruleThickness(geo.h, s.rule_height);
  // the engine's pixel sizes on the 1920×1080 frame, scaled to the preview
  const cqh = (px: number) => `${(px / FRAME_H) * 100}cqh`;
  const cqw = (px: number) => `${(px / FRAME_W) * 100}cqw`;
  const titleSize = cqh(geo.titlePx);
  const indicator = (n: number, k: number, testid: string) =>
    cycle && s.page_indicator && n > 1 ? (
      <span
        className="mkt-strip-preview__page"
        data-testid={testid}
        dir="ltr"
        style={{ fontSize: cqh(geo.indicatorPx), color: s.indicator_color }}
      >
        {pageIndicator(k, n, indic)}
      </span>
    ) : null;

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
        {rule > 0 && (
          <span
            className="mkt-strip-preview__rule"
            data-testid="strip-rule"
            data-edge={s.position === "top" ? "bottom" : "top"}
            style={{
              height: cqh(rule),
              background: s.accent_color,
              [s.position === "top" ? "bottom" : "top"]: 0,
            }}
          />
        )}
        {geo.nameW > 0 && (
          <span
            className="mkt-strip-preview__channel"
            data-testid="strip-channel"
            style={{
              width: cqw(geo.nameW),
              fontSize: titleSize,
              background: s.accent_color,
              color: s.clock_text_color,
            }}
          >
            {channelName}
          </span>
        )}
        <div className="mkt-strip-preview__text" dir={dir} style={{ paddingInline: cqw(geo.pad) }}>
          <span className="mkt-strip-preview__line">
            <span
              className={`mkt-strip-preview__now${
                s.overflow === "scroll" ? " mkt-strip-preview__now--scroll" : ""
              }`}
              data-testid="strip-now"
              data-pages={nowPaging.pages.length}
              style={{ fontSize: titleSize }}
            >
              <span>{nowPaging.pages[ni]}</span>
            </span>
            {indicator(nowPaging.pages.length, ni, "strip-now-page")}
          </span>
          {showNext && (
            <span className="mkt-strip-preview__line">
              <span
                className="mkt-strip-preview__next"
                style={{ fontSize: cqh(geo.nextPx), color: s.next_color }}
                data-testid="strip-next"
                data-pages={nextPaging.pages.length}
              >
                {nextPaging.pages[xi]}
              </span>
              {indicator(nextPaging.pages.length, xi, "strip-next-page")}
            </span>
          )}
        </div>
        {city && (
          <div
            className="mkt-strip-preview__clock"
            data-testid="strip-clock"
            style={{
              background: s.accent_color,
              color: s.clock_text_color,
              width: cqw(geo.clockW),
            }}
          >
            <span className="mkt-strip-preview__city" style={{ fontSize: cqh(geo.clockLabelPx) }}>
              {clockLabel(city, dir)}
            </span>
            <span
              className="mkt-strip-preview__time"
              dir="ltr"
              style={{ fontSize: cqh(geo.clockTimePx) }}
            >
              {formatClock(at, city.zone, s.clock_format, indic)}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
