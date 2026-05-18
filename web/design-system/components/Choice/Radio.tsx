import { forwardRef } from "react";
import type { InputHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import "./choice.css";

// Story 17.2 — Radio + RadioGroup. RadioGroup renders a real <fieldset>
// /<legend> so the group has an accessible name and arrow-key roving
// comes for free from the native radio implementation.

export interface RadioProps
  extends Omit<InputHTMLAttributes<HTMLInputElement>, "id" | "type"> {
  id?: string;
  label?: ReactNode;
}

export const Radio = forwardRef<HTMLInputElement, RadioProps>(function Radio(
  { id, label, className, disabled, ...rest },
  ref
) {
  const radioId = useFieldId(id);
  return (
    <label
      className={clsx("mk-choice mk-choice--radio", disabled && "mk-choice--disabled", className)}
      htmlFor={radioId}
    >
      <input
        ref={ref}
        id={radioId}
        type="radio"
        className="mk-choice__input"
        disabled={disabled}
        {...rest}
      />
      <span className="mk-choice__control" aria-hidden="true">
        <svg className="mk-choice__glyph" viewBox="0 0 16 16" width={8} height={8}>
          <circle cx="8" cy="8" r="6" fill="currentColor" />
        </svg>
      </span>
      {label != null && <span className="mk-choice__label">{label}</span>}
    </label>
  );
});

export interface RadioGroupProps {
  legend: ReactNode;
  /** Shared `name` so the native radios behave as one group. */
  name: string;
  className?: string;
  children: ReactNode;
}

export function RadioGroup({ legend, name, className, children }: RadioGroupProps) {
  return (
    <fieldset className={clsx("mk-radio-group", className)} role="radiogroup" data-name={name}>
      <legend className="mk-radio-group__legend">{legend}</legend>
      {children}
    </fieldset>
  );
}
