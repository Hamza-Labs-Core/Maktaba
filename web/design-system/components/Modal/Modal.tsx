import { useEffect } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import { useFocusTrap } from "../internal/useFocusTrap";
import { useInertBackground } from "../internal/useInertBackground";
import { usePortalHost } from "../internal/usePortalHost";
import "./overlay.css";

// Story 17.2 — Modal. role="dialog" aria-modal, focus trapped + restored
// (useFocusTrap), Esc closes, backdrop click closes, body scroll locked
// while open. Portalled to <body> so it escapes ancestor stacking/
// overflow contexts.

export interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  /** Disable closing on backdrop click (e.g. destructive confirms). */
  dismissable?: boolean;
  className?: string;
}

export function Modal({
  open,
  onClose,
  title,
  children,
  footer,
  dismissable = true,
  className,
}: ModalProps) {
  const ref = useFocusTrap<HTMLDivElement>(open, onClose);
  const titleId = useFieldId();
  const host = usePortalHost(open, "modal");

  useInertBackground(open, host);

  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  if (!open || typeof document === "undefined" || !host) return null;

  return createPortal(
    <div
      className="mk-overlay mk-overlay--center"
      onMouseDown={(e) => {
        if (dismissable && e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={ref}
        className={clsx("mk-modal", className)}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <div className="mk-modal__header">
          <h2 className="mk-modal__title" id={titleId}>
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
        <div className="mk-modal__body">{children}</div>
        {footer != null && <div className="mk-modal__footer">{footer}</div>}
      </div>
    </div>,
    host
  );
}
