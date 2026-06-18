// Lightweight, dependency-free chart primitives for the analytics
// dashboard (Epic 29.3). The web app intentionally ships a minimal dep
// set (React + router only), so rather than pull in recharts we render
// inline SVG. These are presentational and theme-aware via CSS custom
// properties (currentColor / var(--mkt-*)).
import { useId } from "react";

export interface LinePoint {
  label: string;
  value: number;
}

// LineChart plots a single series of (label, value) points as an area +
// line. Width/height are the SVG viewBox; it scales to its container.
export function LineChart({
  points,
  height = 160,
  ariaLabel,
}: {
  points: LinePoint[];
  height?: number;
  ariaLabel: string;
}) {
  const gradId = useId();
  const width = 600;
  const pad = { top: 8, right: 8, bottom: 22, left: 8 };
  const innerW = width - pad.left - pad.right;
  const innerH = height - pad.top - pad.bottom;

  if (points.length === 0) {
    return (
      <svg
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-label={ariaLabel}
        className="mkt-chart"
      >
        <text x={width / 2} y={height / 2} textAnchor="middle" className="mkt-chart__empty">
          —
        </text>
      </svg>
    );
  }

  const max = Math.max(1, ...points.map((p) => p.value));
  const stepX = points.length > 1 ? innerW / (points.length - 1) : 0;
  const x = (i: number) => pad.left + i * stepX;
  const y = (v: number) => pad.top + innerH - (v / max) * innerH;

  const line = points.map((p, i) => `${i === 0 ? "M" : "L"}${x(i)},${y(p.value)}`).join(" ");
  const area = `${line} L${x(points.length - 1)},${pad.top + innerH} L${x(0)},${pad.top + innerH} Z`;

  // Only label a handful of x-ticks to avoid crowding.
  const tickEvery = Math.ceil(points.length / 6);

  return (
    <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={ariaLabel} className="mkt-chart">
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="currentColor" stopOpacity="0.25" />
          <stop offset="100%" stopColor="currentColor" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${gradId})`} className="mkt-chart__area" />
      <path
        d={line}
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        className="mkt-chart__line"
      />
      {points.map((p, i) =>
        i % tickEvery === 0 ? (
          <text key={i} x={x(i)} y={height - 6} textAnchor="middle" className="mkt-chart__tick">
            {p.label}
          </text>
        ) : null
      )}
    </svg>
  );
}

export interface BarItem {
  label: string;
  value: number;
  sub?: string;
}

// BarList renders a ranked horizontal bar list (top videos, genres,
// device breakdown). Values are normalised to the max.
export function BarList({ items, ariaLabel }: { items: BarItem[]; ariaLabel: string }) {
  if (items.length === 0) {
    return <p className="mkt-muted">—</p>;
  }
  const max = Math.max(1, ...items.map((i) => i.value));
  return (
    <ul className="mkt-barlist" aria-label={ariaLabel}>
      {items.map((it, i) => (
        <li key={`${it.label}-${i}`} className="mkt-barlist__row">
          <span className="mkt-barlist__label" dir="auto" title={it.label}>
            {it.label}
          </span>
          <span className="mkt-barlist__track">
            <span className="mkt-barlist__fill" style={{ width: `${(it.value / max) * 100}%` }} />
          </span>
          <span className="mkt-barlist__value">{it.sub ?? it.value}</span>
        </li>
      ))}
    </ul>
  );
}

// Heatmap renders a 7×24 (day-of-week × hour) matrix as a grid of cells
// whose opacity scales with the watched seconds. dayLabels/hourLabel come
// from the caller so the copy is i18n-driven.
export function Heatmap({
  matrix,
  dayLabels,
  ariaLabel,
}: {
  matrix: number[][];
  dayLabels: string[];
  ariaLabel: string;
}) {
  const max = Math.max(1, ...matrix.flat());
  return (
    <div className="mkt-heatmap" role="img" aria-label={ariaLabel}>
      <div className="mkt-heatmap__grid">
        {matrix.map((row, d) => (
          <div key={d} className="mkt-heatmap__row">
            <span className="mkt-heatmap__day">{dayLabels[d] ?? d}</span>
            {row.map((v, h) => (
              <span
                key={h}
                className="mkt-heatmap__cell"
                style={{ opacity: v > 0 ? 0.15 + (v / max) * 0.85 : 0.06 }}
                title={`${dayLabels[d] ?? d} ${h}:00 — ${v}s`}
              />
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}
