// A small SVG line chart (Epic 53 story 53.12): one line per series, time on the x axis
// running left to right in every language (as Arabic charts do), the design system's colour
// tokens so it follows the light and dark themes, a value under the pointer, and a text
// summary for screen readers (the chart itself is hidden from them).
import { useMemo, useState } from "react";

export interface ChartSeries {
  label: string;
  points: [number, number][]; // [unix seconds, value]
}

const COLORS = [
  "var(--color-accent)",
  "var(--color-warn, #b7791f)",
  "var(--color-danger, #c0392b)",
  "var(--color-success, #2f855a)",
  "var(--color-muted, #718096)",
  "var(--color-info, #3182ce)",
];

const W = 600;
const H = 160;
const PAD = 4;

export function LineChart({
  series,
  summary,
  format,
  formatTime,
}: {
  series: ChartSeries[];
  /** what the chart shows, in words (screen readers get this, not the drawing) */
  summary: string;
  format: (v: number) => string;
  formatTime: (t: number) => string;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const box = useMemo(() => {
    let x0 = Infinity;
    let x1 = -Infinity;
    let y1 = 0;
    for (const s of series)
      for (const [t, v] of s.points) {
        x0 = Math.min(x0, t);
        x1 = Math.max(x1, t);
        y1 = Math.max(y1, v);
      }
    if (!Number.isFinite(x0)) return null;
    if (x1 === x0) x1 = x0 + 1;
    return { x0, x1, y1: y1 || 1 };
  }, [series]);
  if (!box) return null;
  const x = (t: number) => PAD + ((t - box.x0) / (box.x1 - box.x0)) * (W - 2 * PAD);
  const y = (v: number) => H - PAD - (v / box.y1) * (H - 2 * PAD);
  const at = hover === null ? null : box.x0 + ((hover - PAD) / (W - 2 * PAD)) * (box.x1 - box.x0);
  const near = (pts: [number, number][]) => {
    if (at === null || pts.length === 0) return null;
    let best = pts[0];
    for (const p of pts) if (Math.abs(p[0] - at) < Math.abs(best[0] - at)) best = p;
    return best;
  };
  return (
    <figure className="mkt-chart" dir="ltr">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        aria-hidden="true"
        className="mkt-chart__svg"
        onMouseMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          setHover(((e.clientX - r.left) / Math.max(1, r.width)) * W);
        }}
        onMouseLeave={() => setHover(null)}
      >
        <line x1={PAD} x2={W - PAD} y1={H - PAD} y2={H - PAD} className="mkt-chart__axis" />
        {series.map((s, i) => (
          <polyline
            key={s.label}
            fill="none"
            stroke={COLORS[i % COLORS.length]}
            strokeWidth={1.5}
            points={s.points.map(([t, v]) => `${x(t).toFixed(1)},${y(v).toFixed(1)}`).join(" ")}
          />
        ))}
        {hover !== null && (
          <line x1={hover} x2={hover} y1={0} y2={H} className="mkt-chart__cursor" />
        )}
      </svg>
      {at !== null && (
        <div className="mkt-chart__tip" data-testid="chart-tip" dir="auto">
          <bdi dir="ltr">{formatTime(at)}</bdi>
          {series.map((s) => {
            const p = near(s.points);
            return p ? (
              <span key={s.label}>
                {s.label}: {format(p[1])}
              </span>
            ) : null;
          })}
        </div>
      )}
      {series.length > 1 && (
        <ul className="mkt-chart__legend" aria-hidden="true" dir="auto">
          {series.map((s, i) => (
            <li key={s.label}>
              <span
                className="mkt-chart__swatch"
                style={{ background: COLORS[i % COLORS.length] }}
              />
              {s.label}
            </li>
          ))}
        </ul>
      )}
      <figcaption className="mkt-visually-hidden" dir="auto">
        {summary}
      </figcaption>
    </figure>
  );
}
