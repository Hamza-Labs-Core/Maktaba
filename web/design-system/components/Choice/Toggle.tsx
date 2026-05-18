import { forwardRef } from "react";
import type { InputHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import "./choice.css";

// Story 17.2 — Toggle (switch). Built on a native checkbox with
// role="switch" so screen readers announce on/off; the visible track +
// thumb is CSS-only and animates via the motion tokens.

export interface ToggleProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, "id" | "type" | "role"> {
  id?: string;
  label?: ReactNode;
}

export const Toggle = forwardRef<HTMLInputElement, ToggleProps>(function Toggle(
  { id, label, className, disabled, ...rest },
  ref
) {
  const toggleId = useFieldId(id);
  return (
    <label
      className={clsx("mk-choice mk-toggle", disabled && "mk-choice--disabled", className)}
      htmlFor={toggleId}
    >
      <input
        ref={ref}
        id={toggleId}
        type="checkbox"
        role="switch"
        className="mk-choice__input"
        disabled={disabled}
        {...rest}
      />
      <span className="mk-choice__control" aria-hidden="true">
        <span className="mk-toggle__thumb" />
      </span>
      {label != null && <span className="mk-choice__label">{label}</span>}
    </label>
  );
});
