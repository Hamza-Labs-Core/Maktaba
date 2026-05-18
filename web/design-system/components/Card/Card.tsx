import { forwardRef } from "react";
import type { HTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "./card.css";

// Story 17.2 — Card. A token-elevated container. Per the EC, an
// overflowing child must produce a scrollbar, never be silently
// clipped: `scrollable` switches the body to `overflow:auto`.
// `interactive` adds hover/focus affordances (and the consumer should
// supply a role/tabIndex or wrap the card content in a link/button).

export interface CardProps extends HTMLAttributes<HTMLDivElement> {
  elevation?: 0 | 1 | 2 | 3;
  interactive?: boolean;
  scrollable?: boolean;
  header?: ReactNode;
  footer?: ReactNode;
  children: ReactNode;
}

export const Card = forwardRef<HTMLDivElement, CardProps>(function Card(
  { elevation = 1, interactive, scrollable, header, footer, className, children, ...rest },
  ref
) {
  return (
    <div
      ref={ref}
      className={clsx(
        "mk-card",
        `mk-card--e${elevation}`,
        interactive && "mk-card--interactive",
        className
      )}
      {...rest}
    >
      {header != null && <div className="mk-card__header">{header}</div>}
      <div className={clsx("mk-card__body", scrollable && "mk-card__body--scroll")}>{children}</div>
      {footer != null && <div className="mk-card__footer">{footer}</div>}
    </div>
  );
});
