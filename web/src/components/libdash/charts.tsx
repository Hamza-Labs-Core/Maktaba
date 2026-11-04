// The library dashboard's and library page's charts, in plain SVG/HTML with
// the queue tokens (dark and light come from the theme). Status colours are
// done / running / waiting / failed and always sit beside a text label or a
// count; every chart has a text alternative (an aria-label, and a table for
// the activity chart).
import { useId, useState } from "react";
import { useI18n } from "../../lib/i18n";

export interface StatusCounts {
  done: number;
  total: number;
  running: number;
  waiting: number;
  failed: number;
}

/**
 * One 100 % bar: done, running, waiting and failed as shares of the units to do
 * (waiting and failed can run past total: the bar's scale is the larger).
 */
export function StatusBar({
  c,
  label,
  className,
}: {
  c: StatusCounts;
  label: string;
  className?: string;
}) {
  const doneShown = Math.min(c.done, c.total);
  const scale = Math.max(c.total, doneShown + c.running + c.waiting + c.failed, 1);
  const w = (v: number) => `${(v / scale) * 100}%`;
  const parts: [keyof StatusCounts, number][] = [
    ["done", doneShown],
    ["running", c.running],
    ["waiting", c.waiting],
    ["failed", c.failed],
  ];
  return (
    <span className={`mkt-ld-bar${className ? ` ${className}` : ""}`} role="img" aria-label={label}>
      {parts
        .filter(([, v]) => v > 0)
        .map(([k, v]) => (
          <span
            key={k}
            className={`mkt-ld-bar__seg mkt-ld-bar__seg--${k}`}
            style={{ inlineSize: w(v) }}
          />
        ))}
    </span>
  );
}

/** The legend of the status colours, in the same order as the bars. */
export function StatusLegend() {
  const { t } = useI18n();
  return (
    <ul className="mkt-ld-legend" aria-label={t("libdash.legend")}>
      {(["done", "running", "waiting", "failed"] as const).map((k) => (
        <li key={k}>
          <span className={`mkt-ld-legend__key mkt-ld-bar__seg--${k}`} aria-hidden="true" />
          {t(`libdash.status.${k}`)}
        </li>
      ))}
    </ul>
  );
}

/** Rounded data-end, square at the baseline (y grows down). */
function column(x: number, y: number, w: number, h: number, r: number): string {
  if (h <= 0) return "";
  const rr = Math.min(r, h, w / 2);
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

export interface ActivityProps {
  days: string[];
  done: number[];
  failed: number[];
  /** a tile's trend: no axis, no table, a summary label */
  compact?: boolean;
  /** what the chart shows, for its name (the library) */
  name: string;
}

/** Jobs finished per day: done columns with the day's failures stacked on top. */
export function ActivityChart({ days, done, failed, compact, name }: ActivityProps) {
  const { t, n, formatDate } = useI18n();
  const tableId = useId();
  const [hover, setHover] = useState<number | null>(null);
  const width = compact ? 140 : 640;
  const height = compact ? 36 : 180;
  const pad = compact
    ? { top: 2, right: 0, bottom: 2, left: 0 }
    : { top: 16, right: 4, bottom: 22, left: 4 };
  const innerW = width - pad.left - pad.right;
  const innerH = height - pad.top - pad.bottom;
  const max = niceMax(Math.max(0, ...days.map((_, i) => (done[i] ?? 0) + (failed[i] ?? 0))));
  const slot = days.length ? innerW / days.length : innerW;
  const bw = Math.min(24, Math.max(2, slot - 2));
  const sum = (a: number[]) => a.reduce((x, y) => x + y, 0);
  const day = (d: string) =>
    formatDate(`${d}T12:00:00Z`, { day: "numeric", month: "short", timeZone: "UTC" });
  const label = t("libdash.activity.aria", {
    name,
    days: n(days.length),
    done: n(sum(done)),
    failed: n(sum(failed)),
  });
  const tip = (i: number) =>
    t("libdash.activity.tip", {
      day: day(days[i]),
      done: n(done[i] ?? 0),
      failed: n(failed[i] ?? 0),
    });

  const bars = days.map((d, i) => {
    const x = pad.left + i * slot + (slot - bw) / 2;
    const dh = ((done[i] ?? 0) / max) * innerH;
    const fh = ((failed[i] ?? 0) / max) * innerH;
    const base = pad.top + innerH;
    const gap = dh > 0 && fh > 0 ? 2 : 0;
    return (
      <g key={d} data-active={hover === i || undefined}>
        <title>{tip(i)}</title>
        <path
          className="mkt-ld-col mkt-ld-col--done"
          d={column(x, base - dh, bw, dh, fh > 0 ? 0 : 4)}
        />
        <path
          className="mkt-ld-col mkt-ld-col--failed"
          d={column(x, base - dh - gap - fh, bw, fh, 4)}
        />
        {!compact && (
          <rect
            className="mkt-ld-col__hit"
            x={pad.left + i * slot}
            y={pad.top}
            width={slot}
            height={innerH}
            onPointerEnter={() => setHover(i)}
            onPointerLeave={() => setHover((h) => (h === i ? null : h))}
          />
        )}
      </g>
    );
  });

  if (compact) {
    return (
      <svg
        className="mkt-ld-spark"
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        role="img"
        aria-label={label}
      >
        <line
          className="mkt-ld-axis"
          x1={0}
          x2={width}
          y1={height - pad.bottom}
          y2={height - pad.bottom}
        />
        {bars}
      </svg>
    );
  }

  return (
    <figure className="mkt-ld-activity">
      <div className="mkt-ld-activity__plot">
        <svg
          viewBox={`0 0 ${width} ${height}`}
          role="img"
          aria-label={label}
          aria-describedby={tableId}
        >
          <line
            className="mkt-ld-gridline"
            x1={pad.left}
            x2={width - pad.right}
            y1={pad.top}
            y2={pad.top}
          />
          <line
            className="mkt-ld-axis"
            x1={pad.left}
            x2={width - pad.right}
            y1={pad.top + innerH}
            y2={pad.top + innerH}
          />
          {bars}
        </svg>
        <span className="mkt-ld-activity__max" aria-hidden="true">
          {n(max, { notation: "compact" })}
        </span>
        {days.length > 0 && (
          <span className="mkt-ld-activity__ends" aria-hidden="true">
            <span>{day(days[0])}</span>
            <span>{day(days[days.length - 1])}</span>
          </span>
        )}
        {hover !== null && (
          <span
            className="mkt-ld-tip"
            role="presentation"
            style={{ insetInlineStart: `${((hover + 0.5) / days.length) * 100}%` }}
          >
            {tip(hover)}
          </span>
        )}
      </div>
      <figcaption className="mkt-ld-legend mkt-ld-legend--inline">
        <span>
          <span className="mkt-ld-legend__key mkt-ld-bar__seg--done" aria-hidden="true" />
          {t("libdash.activity.done")}
        </span>
        <span>
          <span className="mkt-ld-legend__key mkt-ld-bar__seg--failed" aria-hidden="true" />
          {t("libdash.activity.failed")}
        </span>
      </figcaption>
      {/* a table ignores a 1px width, so the hidden box wraps it */}
      <div className="mkt-visually-hidden">
        <table id={tableId}>
          <caption>{label}</caption>
          <thead>
            <tr>
              <th scope="col">{t("libdash.activity.day")}</th>
              <th scope="col">{t("libdash.activity.done")}</th>
              <th scope="col">{t("libdash.activity.failed")}</th>
            </tr>
          </thead>
          <tbody>
            {days.map((d, i) => (
              <tr key={d}>
                <th scope="row">{day(d)}</th>
                <td>{n(done[i] ?? 0)}</td>
                <td>{n(failed[i] ?? 0)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </figure>
  );
}

/** The words of a status bar, for its name and its visible line. */
export function useStatusText() {
  const { t, n } = useI18n();
  return (c: StatusCounts) =>
    t("libdash.bar.aria", {
      done: n(Math.min(c.done, c.total)),
      total: n(c.total),
      running: n(c.running),
      waiting: n(c.waiting),
      failed: n(c.failed),
    });
}
