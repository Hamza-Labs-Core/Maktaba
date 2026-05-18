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

  // Chain every wrapped handler: run the tooltip's behaviour, then the
  // child's own pre-existing handler (a wrapped trigger may already
  // have onFocus / onMouseEnter / etc — those must not be silently
  // dropped). Mirrors how onKeyDown was already chained.
  // NOTE(wave-1): hover/focus only; touch tap-to-toggle is a separate
  // enhancement (known hover-tooltip limitation, deferred).
  const childProps = children.props as {
    onMouseEnter?: (e: React.MouseEvent) => void;
    onMouseLeave?: (e: React.MouseEvent) => void;
    onFocus?: (e: React.FocusEvent) => void;
    onBlur?: (e: React.FocusEvent) => void;
    onKeyDown?: (e: React.KeyboardEvent) => void;
  };

  const trigger = cloneElement(children, {
    "aria-describedby": open ? id : undefined,
    onMouseEnter: (e: React.MouseEvent) => {
      setOpen(true);
      childProps.onMouseEnter?.(e);
    },
    onMouseLeave: (e: React.MouseEvent) => {
      setOpen(false);
      childProps.onMouseLeave?.(e);
    },
    onFocus: (e: React.FocusEvent) => {
      setOpen(true);
      childProps.onFocus?.(e);
    },
    onBlur: (e: React.FocusEvent) => {
      setOpen(false);
      childProps.onBlur?.(e);
    },
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
      childProps.onKeyDown?.(e);
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
