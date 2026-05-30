import type { HTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "./badge.css";

// Story 17.2 — Badge. A small non-interactive status label. The tone
// maps onto the semantic colour tokens so dark/high-contrast themes are
// automatic.

type Tone = "neutral" | "accent" | "success" | "warn" | "error";

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: Tone;
  children: ReactNode;
}

export function Badge({ tone = "neutral", className, children, ...rest }: BadgeProps) {
  return (
    <span className={clsx("mk-badge", `mk-badge--${tone}`, className)} {...rest}>
      {children}
    </span>
  );
}
