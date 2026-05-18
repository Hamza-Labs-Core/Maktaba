import type { ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "../EmptyState/states.css";

// Story 17.2 / 17.5 — ErrorState. Classified error surface. `kind` maps
// onto the five spec categories so callers can branch illustration/copy;
// the region is role="alert" so the failure is announced. Strings come
// from the consumer (i18n) per the Epic-17 "no prose in JSX" rule.

export type ErrorKind =
  | "network"
  | "server"
  | "permission"
  | "not_found"
  | "validation";

export interface ErrorStateProps {
  kind: ErrorKind;
  title: ReactNode;
  description?: ReactNode;
  icon?: ReactNode;
  /** Typically a "Retry" Button — the consumer owns idempotent retry. */
  action?: ReactNode;
  className?: string;
}

export function ErrorState({
  kind,
  title,
  description,
  icon,
  action,
  className,
}: ErrorStateProps) {
  return (
    <div
      className={clsx("mk-state mk-state--error", className)}
      data-kind={kind}
      role="alert"
    >
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
