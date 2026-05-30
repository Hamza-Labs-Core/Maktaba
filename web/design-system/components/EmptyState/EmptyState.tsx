import type { ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "./states.css";

// Story 17.2 / 17.5 — EmptyState. A centred title + body + optional CTA
// for "nothing here yet" surfaces. `kind` tags the cause so callers can
// localise distinct copy (first_run vs filtered_out vs cleared); it is
// surface metadata only — the strings are passed in (i18n stays with the
// consumer per the Epic-17 "no prose in JSX" rule).

export type EmptyKind = "first_run" | "filtered_out" | "cleared";

export interface EmptyStateProps {
  kind?: EmptyKind;
  title: ReactNode;
  description?: ReactNode;
  /** Visual only; decorative, hidden from AT. */
  icon?: ReactNode;
  action?: ReactNode;
  className?: string;
}

export function EmptyState({
  kind = "first_run",
  title,
  description,
  icon,
  action,
  className,
}: EmptyStateProps) {
  return (
    <div className={clsx("mk-state", className)} data-kind={kind}>
      {icon && (
        <div className="mk-state__icon" aria-hidden="true">
          {icon}
        </div>
      )}
      <p className="mk-state__title">{title}</p>
      {description != null && <p className="mk-state__desc">{description}</p>}
      {action && <div className="mk-state__action">{action}</div>}
    </div>
  );
}
