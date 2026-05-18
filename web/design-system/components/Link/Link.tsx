import { forwardRef } from "react";
import type { AnchorHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "../internal/base.css";
import "./link.css";

// Story 17.2 — Link. A token-styled <a>. When `external` is set it adds
// rel="noopener noreferrer" and a visually-hidden "(opens in new tab)"
// hint so the new-tab behaviour is announced.

export interface LinkProps extends AnchorHTMLAttributes<HTMLAnchorElement> {
  external?: boolean;
  children: ReactNode;
}

export const Link = forwardRef<HTMLAnchorElement, LinkProps>(function Link(
  { external, className, children, target, rel, ...rest },
  ref
) {
  return (
    <a
      ref={ref}
      className={clsx("mk-link", className)}
      target={external ? "_blank" : target}
      rel={external ? "noopener noreferrer" : rel}
      {...rest}
    >
      {children}
      {external && <span className="mk-visually-hidden"> (opens in new tab)</span>}
    </a>
  );
});
