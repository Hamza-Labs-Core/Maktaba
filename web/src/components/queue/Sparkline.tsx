// Tiny trend line for the health tiles (plan 06-11 §9.2). Pure SVG,
// stroke follows currentColor so it takes the tile's token colour.
export interface SparklineProps {
  values: number[];
  /** Accessible name, e.g. "NAS open time, last 4 hours". */
  label: string;
  width?: number;
  height?: number;
}

export function Sparkline({ values, label, width = 120, height = 32 }: SparklineProps) {
  const pad = 2;
  const clean = values.filter((v) => Number.isFinite(v));
  const max = Math.max(1, ...clean);
  const min = Math.min(0, ...clean);
  const span = max - min || 1;
  const step = clean.length > 1 ? (width - pad * 2) / (clean.length - 1) : 0;
  const pts = clean.map((v, i) => [
    pad + i * step,
    pad + (height - pad * 2) * (1 - (v - min) / span),
  ]);
  const d = pts
    .map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`)
    .join(" ");
  const area =
    pts.length > 1
      ? `${d} L${pts[pts.length - 1][0].toFixed(1)},${height - pad} L${pts[0][0].toFixed(1)},${height - pad} Z`
      : "";
  return (
    <svg
      className="mkt-q-spark"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      role="img"
      aria-label={label}
    >
      <line
        className="mkt-q-spark__base"
        x1={pad}
        x2={width - pad}
        y1={height - pad}
        y2={height - pad}
      />
      {area && <path className="mkt-q-spark__area" d={area} />}
      {pts.length > 1 && <path className="mkt-q-spark__line" d={d} fill="none" />}
    </svg>
  );
}
