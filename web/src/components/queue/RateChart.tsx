// "Done per 10 minutes" chart: bars of finished jobs, failures stacked on
// top, and a dashed line (own scale) for NAS timeouts. Inline SVG with
// CSS classes, no chart library; colours come from tokens in queue.css.
import { useI18n } from "../../lib/i18n";
import type { RatePoint } from "../../lib/queueApi";

export interface RateChartProps {
  points: RatePoint[];
  height?: number;
}

export function RateChart({ points, height = 170 }: RateChartProps) {
  const { t, n } = useI18n();
  const width = 600;
  const pad = { top: 10, right: 8, bottom: 20, left: 8 };
  const innerW = width - pad.left - pad.right;
  const innerH = height - pad.top - pad.bottom;
  const max = Math.max(1, ...points.map((p) => p.done + p.failed));
  const maxTimeouts = Math.max(1, ...points.map((p) => p.nas_timeouts));
  const slot = points.length ? innerW / points.length : innerW;
  const bw = Math.max(1, slot * 0.72);
  const y = (v: number) => pad.top + innerH - (v / max) * innerH;
  const totalDone = points.reduce((a, p) => a + p.done, 0);
  const totalFailed = points.reduce((a, p) => a + p.failed, 0);
  const totalTimeouts = points.reduce((a, p) => a + p.nas_timeouts, 0);
  const aria = t("queue.chart.aria", {
    done: n(totalDone),
    failed: n(totalFailed),
    timeouts: n(totalTimeouts),
  });
  const line = points
    .map((p, i) => {
      const x = pad.left + i * slot + slot / 2;
      const yy = pad.top + innerH - (p.nas_timeouts / maxTimeouts) * innerH;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${yy.toFixed(1)}`;
    })
    .join(" ");
  return (
    <figure className="mkt-q-chart">
      <svg
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-label={aria}
        className="mkt-q-chart__svg"
      >
        <line
          className="mkt-q-chart__axis"
          x1={pad.left}
          x2={width - pad.right}
          y1={pad.top + innerH}
          y2={pad.top + innerH}
        />
        {points.map((p, i) => {
          const x = pad.left + i * slot + (slot - bw) / 2;
          const doneH = (p.done / max) * innerH;
          const failH = (p.failed / max) * innerH;
          return (
            <g key={p.t}>
              <title>
                {t("queue.chart.tip", {
                  time: new Date(p.t).toLocaleTimeString([], {
                    hour: "2-digit",
                    minute: "2-digit",
                  }),
                  done: n(p.done),
                  failed: n(p.failed),
                })}
              </title>
              <rect
                className="mkt-q-bar mkt-q-bar--done"
                x={x}
                y={y(p.done)}
                width={bw}
                height={doneH}
              />
              {p.failed > 0 && (
                <rect
                  className="mkt-q-bar mkt-q-bar--failed"
                  x={x}
                  y={y(p.done) - failH}
                  width={bw}
                  height={failH}
                />
              )}
            </g>
          );
        })}
        {totalTimeouts > 0 && <path className="mkt-q-chart__timeouts" d={line} fill="none" />}
      </svg>
      <figcaption className="mkt-q-legend">
        <span className="mkt-q-legend__item mkt-q-legend__item--done">{t("queue.chart.done")}</span>
        <span className="mkt-q-legend__item mkt-q-legend__item--failed">
          {t("queue.chart.failed")}
        </span>
        <span className="mkt-q-legend__item mkt-q-legend__item--timeouts">
          {t("queue.chart.timeouts")}
        </span>
      </figcaption>
    </figure>
  );
}
