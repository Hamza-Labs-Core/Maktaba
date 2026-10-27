// Epic 45 Phase 3 (story 45.19) — scrub thumbnails (trickplay) over the
// player's progress bar.
//
// GET /api/videos/{id}/trickplay gives the video's sprite sheet and its grid:
// tile i shows the video at i * interval_sec. While the pointer is over the
// bottom of the video (where the browser draws its progress bar) the tile of
// the time under the pointer shows above it, with the time; while the viewer
// drags the bar (the element is seeking) the tile of the seek's time shows.
// A video without a sheet (404, an older server) shows nothing and plays as
// before. The browser's own timeline runs left to right in either direction
// of the page, so the pointer's position is read from the left.
import { useEffect, useState, type CSSProperties } from "react";
import { api } from "../lib/api";
import { formatDuration } from "./VideoViews";

export interface TrickplayIndex {
  sprite_url: string;
  columns: number;
  rows: number;
  count: number;
  interval_sec: number;
  tile_width: number;
  tile_height: number;
  sheet_width: number;
  sheet_height: number;
}

/** The height (px) of the strip at the video's bottom that counts as the progress bar. */
export const BAR_BAND_PX = 48;
/** How wide the preview is drawn (the sheet's tiles are scaled to it). */
const PREVIEW_WIDTH = 176;

export async function fetchTrickplay(videoId: string): Promise<TrickplayIndex | null> {
  try {
    const r = await api.get<TrickplayIndex>(`/api/videos/${encodeURIComponent(videoId)}/trickplay`);
    const ok =
      r &&
      typeof r.sprite_url === "string" &&
      r.columns > 0 &&
      r.rows > 0 &&
      r.interval_sec > 0 &&
      r.tile_width > 0 &&
      r.tile_height > 0;
    return ok ? r : null;
  } catch {
    return null; // no sheet: the player plays as before
  }
}

/** The tile showing time `at` (seconds): its column and row. */
export function tileAt(ix: TrickplayIndex, at: number): { col: number; row: number } {
  const count = Math.max(1, Math.min(ix.count || ix.columns * ix.rows, ix.columns * ix.rows));
  const i = Math.min(count - 1, Math.max(0, Math.floor(at / ix.interval_sec)));
  return { col: i % ix.columns, row: Math.floor(i / ix.columns) };
}

/** The style that shows the tile of time `at`, scaled to `width` px. */
export function tileStyle(ix: TrickplayIndex, at: number, width = PREVIEW_WIDTH): CSSProperties {
  const { col, row } = tileAt(ix, at);
  const scale = width / ix.tile_width;
  // whole pixels: no seam of the next tile, and "0" rather than "-0"
  const px = (v: number) => `${Math.round(v) || 0}px`;
  return {
    inlineSize: px(width),
    blockSize: px(ix.tile_height * scale),
    backgroundImage: `url("${ix.sprite_url}")`,
    backgroundSize: `${px(ix.sheet_width * scale)} ${px(ix.sheet_height * scale)}`,
    backgroundPosition: `${px(-col * ix.tile_width * scale)} ${px(-row * ix.tile_height * scale)}`,
  };
}

interface Shown {
  at: number;
  /** the preview's centre, px from the video's left edge */
  x: number;
}

export function ScrubPreview({ videoId, el }: { videoId: string; el: HTMLVideoElement | null }) {
  const [ix, setIx] = useState<TrickplayIndex | null>(null);
  const [shown, setShown] = useState<Shown | null>(null);

  useEffect(() => {
    let cancelled = false;
    setIx(null);
    fetchTrickplay(videoId).then((r) => !cancelled && setIx(r));
    return () => {
      cancelled = true;
    };
  }, [videoId]);

  useEffect(() => {
    if (!el || !ix) return;
    const duration = () => (Number.isFinite(el.duration) && el.duration > 0 ? el.duration : 0);
    let hovering = false;
    const onMove = (e: PointerEvent) => {
      const r = el.getBoundingClientRect();
      const d = duration();
      hovering =
        d > 0 && r.width > 0 && e.clientY >= r.bottom - BAR_BAND_PX && e.clientY <= r.bottom;
      if (!hovering) {
        if (!el.seeking) setShown(null);
        return;
      }
      const frac = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
      setShown({ at: frac * d, x: e.clientX - r.left });
    };
    const onLeave = () => {
      hovering = false;
      if (!el.seeking) setShown(null);
    };
    // dragging the bar (or a key): the seek's own time, placed on the bar
    const onSeeking = () => {
      const d = duration();
      const r = el.getBoundingClientRect();
      if (d <= 0 || r.width <= 0) return;
      setShown({ at: el.currentTime, x: (el.currentTime / d) * r.width });
    };
    const onSeeked = () => {
      if (!hovering) setShown(null);
    };
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerleave", onLeave);
    el.addEventListener("seeking", onSeeking);
    el.addEventListener("seeked", onSeeked);
    return () => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerleave", onLeave);
      el.removeEventListener("seeking", onSeeking);
      el.removeEventListener("seeked", onSeeked);
    };
  }, [el, ix]);

  if (!ix || !shown || !el) return null;
  const width = el.getBoundingClientRect().width;
  const half = PREVIEW_WIDTH / 2;
  // kept inside the video
  const left = Math.min(Math.max(0, shown.x - half), Math.max(0, width - PREVIEW_WIDTH));
  return (
    <div
      className="mkt-scrub"
      style={{ left: `${left}px`, bottom: `${BAR_BAND_PX + 8}px` }}
      aria-hidden="true"
      data-testid="scrub-preview"
    >
      <div className="mkt-scrub__tile" style={tileStyle(ix, shown.at)} />
      <span className="mkt-scrub__time">{formatDuration(shown.at)}</span>
    </div>
  );
}
