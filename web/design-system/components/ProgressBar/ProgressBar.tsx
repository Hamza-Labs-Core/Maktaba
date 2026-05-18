import { clsx } from "../internal/clsx";
import "./progress-bar.css";

// Story 17.2 / 17.10 — ProgressBar. Determinate by default with a
// role="progressbar" + aria-valuenow/min/max; pass `indeterminate` for
// an animated unknown-duration bar (then the aria-value* are omitted per
// ARIA spec).

export interface ProgressBarProps {
  /** 0–100. Ignored when `indeterminate`. */
  value?: number;
  indeterminate?: boolean;
  /** Accessible name for the progress region. */
  label: string;
  tone?: "accent" | "success";
  className?: string;
}

export function ProgressBar({
  value = 0,
  indeterminate,
  label,
  tone = "accent",
  className,
}: ProgressBarProps) {
  const clamped = Math.min(100, Math.max(0, value));
  return (
    <div
      className={clsx("mk-progress", indeterminate && "mk-progress--indeterminate", className)}
      role="progressbar"
      aria-label={label}
      aria-valuemin={indeterminate ? undefined : 0}
      aria-valuemax={indeterminate ? undefined : 100}
      aria-valuenow={indeterminate ? undefined : Math.round(clamped)}
    >
      <div
        className={clsx("mk-progress__fill", `mk-progress__fill--${tone}`)}
        style={indeterminate ? undefined : { inlineSize: `${clamped}%` }}
      />
    </div>
  );
}
