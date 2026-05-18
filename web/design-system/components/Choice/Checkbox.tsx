import { forwardRef } from "react";
import type { InputHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import "./choice.css";

// Story 17.2 — Checkbox. A real native <input type=checkbox> is kept
// visually hidden (not removed) so it stays keyboard-focusable and in
// the accessibility tree; the painted box is a CSS sibling.

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "id" | "type"> {
  id?: string;
  label?: ReactNode;
}

export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(function Checkbox(
  { id, label, className, disabled, ...rest },
  ref
) {
  const boxId = useFieldId(id);
  return (
    <label
      className={clsx(
        "mk-choice mk-choice--checkbox",
        disabled && "mk-choice--disabled",
        className
      )}
      htmlFor={boxId}
    >
      <input
        ref={ref}
        id={boxId}
        type="checkbox"
        className="mk-choice__input"
        disabled={disabled}
        {...rest}
      />
      <span className="mk-choice__control" aria-hidden="true">
        <svg className="mk-choice__glyph" viewBox="0 0 16 16" width={12} height={12}>
          <path
            d="M3 8.5l3.2 3.2L13 5"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </span>
      {label != null && <span className="mk-choice__label">{label}</span>}
    </label>
  );
});
