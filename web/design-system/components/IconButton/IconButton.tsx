import { forwardRef } from "react";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "../Button/button.css";
import "./icon-button.css";

// Story 17.2 — IconButton. A square, icon-only action. `label` is
// mandatory and becomes the accessible name (aria-label) because the
// visible content carries no text.

type Variant = "primary" | "secondary" | "ghost" | "destructive";
type Size = "sm" | "md" | "lg";

export interface IconButtonProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "aria-label"> {
  /** Accessible name — required since the button has no visible text. */
  label: string;
  icon: ReactNode;
  variant?: Variant;
  size?: Size;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { label, icon, variant = "ghost", size = "md", className, ...rest },
  ref
) {
  return (
    <button
      ref={ref}
      type="button"
      aria-label={label}
      className={clsx(
        "mk-btn",
        "mk-icon-btn",
        `mk-btn--${variant}`,
        `mk-icon-btn--${size}`,
        className
      )}
      {...rest}
    >
      <span className="mk-icon-btn__glyph" aria-hidden="true">
        {icon}
      </span>
    </button>
  );
});
