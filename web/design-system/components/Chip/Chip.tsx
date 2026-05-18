import type { HTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "./chip.css";

// Story 17.2 — Chip. A compact, optionally-removable / selectable tag
// (used for facets and filters in search). When `onRemove` is given it
// renders a real <button> with an accessible label so the remove action
// is keyboard-operable.

export interface ChipProps extends Omit<HTMLAttributes<HTMLSpanElement>, "onSelect"> {
  selected?: boolean;
  onRemove?: () => void;
  removeLabel?: string;
  children: ReactNode;
}

export function Chip({
  selected,
  onRemove,
  removeLabel = "Remove",
  className,
  children,
  ...rest
}: ChipProps) {
  return (
    <span
      className={clsx("mk-chip", selected && "mk-chip--selected", className)}
      data-selected={selected || undefined}
      {...rest}
    >
      <span className="mk-chip__label">{children}</span>
      {onRemove && (
        <button
          type="button"
          className="mk-chip__remove"
          aria-label={removeLabel}
          onClick={onRemove}
        >
          <svg viewBox="0 0 16 16" width={12} height={12} aria-hidden="true">
            <path
              d="M4 4l8 8M12 4l-8 8"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
            />
          </svg>
        </button>
      )}
    </span>
  );
}
