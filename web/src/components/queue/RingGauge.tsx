// Ring gauge: done / total of the current backlog wave. Inline SVG, no
// chart library; the colour comes from a CSS class (tokens only) and the
// arc is not animated, so prefers-reduced-motion needs no special case.
import { ringDash, percent, type QueueVisualState } from "./format";

export interface RingGaugeProps {
  done: number;
  total: number;
  state: QueueVisualState;
  /** Accessible description, e.g. "2,410 of 2,752 done". */
  label: string;
  size?: number;
  /** Centre text; defaults to the rounded percentage. */
  centre?: string;
}

export function RingGauge({ done, total, state, label, size = 56, centre }: RingGaugeProps) {
  const stroke = Math.max(4, Math.round(size / 9));
  const r = (size - stroke) / 2;
  const [dash, circ] = ringDash(done, total, r);
  return (
    <svg
      className={`mkt-q-ring mkt-q-ring--${state}`}
      viewBox={`0 0 ${size} ${size}`}
      width={size}
      height={size}
      role="img"
      aria-label={label}
      data-testid="ring-gauge"
      data-dash={dash.toFixed(2)}
    >
      <circle
        className="mkt-q-ring__track"
        cx={size / 2}
        cy={size / 2}
        r={r}
        strokeWidth={stroke}
        fill="none"
      />
      <circle
        className="mkt-q-ring__fg"
        cx={size / 2}
        cy={size / 2}
        r={r}
        strokeWidth={stroke}
        fill="none"
        strokeLinecap="round"
        strokeDasharray={`${dash} ${circ}`}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
      />
      <text
        className="mkt-q-ring__text"
        x="50%"
        y="50%"
        textAnchor="middle"
        dominantBaseline="central"
        aria-hidden="true"
      >
        {centre ?? `${percent(done, total)}%`}
      </text>
    </svg>
  );
}
