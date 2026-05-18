import { useEffect } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import { useFocusTrap } from "../internal/useFocusTrap";
import "../Modal/overlay.css";

// Story 17.2 — Drawer. Edge-anchored panel sharing Modal's focus-trap +
// Esc + backdrop + scroll-lock behaviour. `side` is logical ("end" =
// inline-end) so it mirrors correctly under RTL.

export interface DrawerProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  side?: "start" | "end";
  children: ReactNode;
  className?: string;
}

export function Drawer({
  open,
  onClose,
  title,
  side = "end",
  children,
  className,
}: DrawerProps) {
  const ref = useFocusTrap<HTMLDivElement>(open, onClose);
  const titleId = useFieldId();

  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  if (!open || typeof document === "undefined") return null;

  return createPortal(
    <div
      className={clsx("mk-overlay", `mk-overlay--drawer-${side}`)}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={ref}
        className={clsx("mk-drawer", className)}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <div className="mk-drawer__header">
          <h2 className="mk-drawer__title" id={titleId}>
            {title}
          </h2>
          <button
            type="button"
            className="mk-overlay__close"
            aria-label="Close"
            onClick={onClose}
          >
            <svg viewBox="0 0 16 16" width={16} height={16} aria-hidden="true">
              <path
                d="M4 4l8 8M12 4l-8 8"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
        <div className="mk-drawer__body">{children}</div>
      </div>
    </div>,
    document.body
  );
}
