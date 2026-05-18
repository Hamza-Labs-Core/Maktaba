import { cloneElement, useId, useState } from "react";
import type { ReactElement, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "./tooltip.css";

// Story 17.2 — Tooltip. Wraps a single focusable trigger element and
// shows a label on hover AND keyboard focus (a11y: never hover-only).
// The tip is wired via aria-describedby; Escape dismisses it. The
// trigger must accept a ref-less set of DOM props (button/a/etc).

export interface TooltipProps {
  label: ReactNode;
  children: ReactElement;
  side?: "top" | "bottom";
  className?: string;
}

export function Tooltip({ label, children, side = "top", className }: TooltipProps) {
  const [open, setOpen] = useState(false);
  const id = useId();

  const trigger = cloneElement(children, {
    "aria-describedby": open ? id : undefined,
    onMouseEnter: () => setOpen(true),
    onMouseLeave: () => setOpen(false),
    onFocus: () => setOpen(true),
    onBlur: () => setOpen(false),
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
      (children.props as { onKeyDown?: (e: React.KeyboardEvent) => void }).onKeyDown?.(e);
    },
  });

  return (
    <span className={clsx("mk-tooltip-wrap", className)}>
      {trigger}
      <span
        role="tooltip"
        id={id}
        className={clsx("mk-tooltip", `mk-tooltip--${side}`)}
        data-open={open || undefined}
        hidden={!open}
      >
        {label}
      </span>
    </span>
  );
}
