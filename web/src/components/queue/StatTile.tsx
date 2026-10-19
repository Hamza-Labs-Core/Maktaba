// The one counter tile of the queue pages: the health strip, "Library at a
// glance" and a stage's KPIs all draw it, so every tile has the same box,
// label, number (tabular figures) and unit. Optional detail lines sit under
// the number and an optional sparkline is pinned to the bottom, so tiles in
// one row line up whatever they carry.
import { useId } from "react";
import type { ReactNode } from "react";
import { Sparkline } from "./Sparkline";

export interface StatTileProps {
  label: string;
  /** The number, already formatted ("1,200", "47", "3 h 20"). */
  value: ReactNode;
  /** Small words after the number ("jobs waiting", "%"). */
  unit?: ReactNode;
  tone?: "warn" | "error";
  spark?: { values: number[]; label: string };
  /** Draw the label as a heading (the health strip's tiles are page sections). */
  heading?: "h2" | "h3";
  /** Detail lines under the number. */
  children?: ReactNode;
}

export function StatTile({ label, value, unit, tone, spark, heading, children }: StatTileProps) {
  const id = useId();
  const Label = heading ?? "span";
  return (
    <div
      className={`mkt-q-stat${tone ? ` mkt-q-stat--${tone}` : ""}`}
      role="group"
      aria-labelledby={id}
    >
      <Label className="mkt-q-stat__label" id={id}>
        {tone && (
          <span className="mkt-q-stat__flag" aria-hidden="true">
            !
          </span>
        )}
        {label}
      </Label>
      <span className="mkt-q-stat__value">
        <span className="mkt-q-stat__num">{value}</span>
        {unit !== undefined && <span className="mkt-q-stat__unit">{unit}</span>}
      </span>
      {children !== undefined && <div className="mkt-q-stat__detail">{children}</div>}
      {spark && <Sparkline values={spark.values} label={spark.label} />}
    </div>
  );
}

/** A grid of tiles: equal widths, and every row as tall as the tallest. */
export function StatGrid({
  label,
  variant,
  children,
}: {
  label?: string;
  variant?: "health";
  children: ReactNode;
}) {
  return (
    <div
      className={`mkt-q-stats${variant ? ` mkt-q-stats--${variant}` : ""}`}
      role={label ? "group" : undefined}
      aria-label={label}
    >
      {children}
    </div>
  );
}
