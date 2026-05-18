import type { CSSProperties } from "react";
import { clsx } from "../internal/clsx";
import "./skeleton.css";

// Story 17.2 / 17.4 — Skeleton. A shape-matching loading placeholder.
// Marked aria-hidden + role=presentation so assistive tech ignores the
// flicker; the container should expose its own aria-busy/live status.
// The shimmer is disabled under prefers-reduced-motion (see CSS).

export interface SkeletonProps {
  /** Shape preset. `text` renders a short rounded bar. */
  variant?: "text" | "rect" | "circle";
  width?: number | string;
  height?: number | string;
  /** Number of stacked lines (text variant only). */
  lines?: number;
  className?: string;
}

export function Skeleton({
  variant = "text",
  width,
  height,
  lines = 1,
  className,
}: SkeletonProps) {
  const style: CSSProperties = {
    width: width ?? (variant === "circle" ? 40 : undefined),
    height: height ?? (variant === "circle" ? 40 : undefined),
  };
  if (variant === "text" && lines > 1) {
    return (
      <span
        className={clsx("mk-skeleton-stack", className)}
        aria-hidden="true"
        role="presentation"
      >
        {Array.from({ length: lines }, (_, i) => (
          <span
            key={i}
            className="mk-skeleton mk-skeleton--text"
            style={{ width: i === lines - 1 ? "60%" : width }}
          />
        ))}
      </span>
    );
  }
  return (
    <span
      className={clsx("mk-skeleton", `mk-skeleton--${variant}`, className)}
      style={style}
      aria-hidden="true"
      role="presentation"
    />
  );
}
