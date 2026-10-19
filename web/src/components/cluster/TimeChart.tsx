// A small multi-series line chart over time (inline SVG, no chart
// dependency, like components/charts). Colours come from tokens via the
// series index class; the legend under it names each line with its latest
// value, so the numbers are readable without the picture.
export interface ChartSeries {
  key: string;
  label: string;
  /** one value per time; null leaves a gap */
  values: (number | null)[];
}

export interface TimeChartProps {
  /** epoch ms, oldest first */
  times: number[];
  series: ChartSeries[];
  /** accessible name of the chart */
  label: string;
  format: (v: number) => string;
  formatTime: (ms: number) => string;
  height?: number;
  emptyText: string;
}

export function TimeChart({
  times,
  series,
  label,
  format,
  formatTime,
  height = 140,
  emptyText,
}: TimeChartProps) {
  const width = 600;
  const pad = { top: 8, bottom: 20, side: 4 };
  const innerH = height - pad.top - pad.bottom;
  const all = series.flatMap((s) => s.values.filter((v): v is number => v !== null));
  if (times.length < 2 || all.length === 0) {
    return <p className="mkt-mc-muted">{emptyText}</p>;
  }
  const max = Math.max(1, ...all);
  const t0 = times[0];
  const span = Math.max(1, times[times.length - 1] - t0);
  const x = (t: number) => pad.side + ((t - t0) / span) * (width - pad.side * 2);
  const y = (v: number) => pad.top + innerH - (v / max) * innerH;
  const path = (values: (number | null)[]) => {
    let d = "";
    let pen = false;
    values.forEach((v, i) => {
      if (v === null || i >= times.length) {
        pen = false;
        return;
      }
      d += `${pen ? "L" : "M"}${x(times[i]).toFixed(1)},${y(v).toFixed(1)} `;
      pen = true;
    });
    return d.trim();
  };
  const latest = (values: (number | null)[]) => {
    for (let i = values.length - 1; i >= 0; i--) if (values[i] !== null) return values[i];
    return null;
  };
  return (
    <figure className="mkt-cl-chart">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={label}>
        <line
          className="mkt-cl-chart__base"
          x1={pad.side}
          x2={width - pad.side}
          y1={pad.top + innerH}
          y2={pad.top + innerH}
        />
        {series.map((s, i) => (
          <path
            key={s.key}
            className={`mkt-cl-chart__line mkt-cl-chart__line--${i % 4}`}
            d={path(s.values)}
            fill="none"
          />
        ))}
        <text className="mkt-cl-chart__tick" x={pad.side} y={height - 4} textAnchor="start">
          {formatTime(times[0])}
        </text>
        <text className="mkt-cl-chart__tick" x={width - pad.side} y={height - 4} textAnchor="end">
          {formatTime(times[times.length - 1])}
        </text>
        <text className="mkt-cl-chart__tick" x={pad.side} y={pad.top + 10} textAnchor="start">
          {format(max)}
        </text>
      </svg>
      <figcaption>
        <ul className="mkt-cl-legend">
          {series.map((s, i) => {
            const v = latest(s.values);
            return (
              <li key={s.key}>
                <span
                  className={`mkt-cl-legend__swatch mkt-cl-chart__line--${i % 4}`}
                  aria-hidden="true"
                />
                {s.label}: <strong>{v === null ? "–" : format(v)}</strong>
              </li>
            );
          })}
        </ul>
      </figcaption>
    </figure>
  );
}
