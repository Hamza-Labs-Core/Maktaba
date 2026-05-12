import { forwardRef } from "react";
import type { ButtonHTMLAttributes, ReactNode } from "react";
import "./button.css";

// Story 17.2 — Button. The 5 variants × 3 sizes are the AC-1 surface.
// `loading` reserves the label width so the button doesn't reflow when
// its state flips, and `disabled` is forced-true while loading so the
// click handler can't fire mid-action.

type Variant = "primary" | "secondary" | "ghost" | "destructive" | "link";
type Size = "sm" | "md" | "lg";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  leadingIcon?: ReactNode;
  trailingIcon?: ReactNode;
}

function clsx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    variant = "primary",
    size = "md",
    loading,
    leadingIcon,
    trailingIcon,
    disabled,
    className,
    children,
    ...rest
  },
  ref
) {
  const isDisabled = disabled || loading;
  return (
    <button
      ref={ref}
      disabled={isDisabled}
      aria-busy={loading || undefined}
      className={clsx(
        "mk-btn",
        `mk-btn--${variant}`,
        `mk-btn--${size}`,
        loading && "is-loading",
        className
      )}
      {...rest}
    >
      {loading ? <Spinner aria-hidden="true" /> : leadingIcon}
      <span className="mk-btn__label">{children}</span>
      {!loading && trailingIcon}
    </button>
  );
});

function Spinner(props: { "aria-hidden": "true" | "false" }) {
  return (
    <span className="mk-btn__spinner" aria-hidden={props["aria-hidden"]}>
      {/* SVG kept inline so the component doesn't need an icon system
          to render its loading state. The animation runs from CSS. */}
      <svg viewBox="0 0 16 16" width={14} height={14}>
        <circle
          cx="8"
          cy="8"
          r="6"
          fill="none"
          strokeWidth="2"
          stroke="currentColor"
          opacity="0.25"
        />
        <path d="M14 8a6 6 0 0 0-6-6" fill="none" strokeWidth="2" stroke="currentColor" />
      </svg>
    </span>
  );
}
