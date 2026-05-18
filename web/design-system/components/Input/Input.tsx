import { forwardRef } from "react";
import type { InputHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import { FieldShell, fieldAria } from "../Field/Field";
import "../Field/field.css";

// Story 17.2 — Input. Wraps a native <input> in the shared FieldShell so
// label/description/error a11y wiring is identical to every other form
// primitive. The control keeps the `mk-control` surface class so theme
// switches cascade automatically.

export interface InputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "id"> {
  /** Pin a stable id (else an SSR-safe one is generated). */
  id?: string;
  label?: ReactNode;
  description?: ReactNode;
  error?: ReactNode;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { id, label, description, error, required, className, ...rest },
  ref
) {
  const inputId = useFieldId(id);
  return (
    <FieldShell
      htmlFor={inputId}
      label={label}
      description={description}
      error={error}
      required={required}
    >
      <input
        ref={ref}
        id={inputId}
        required={required}
        className={clsx("mk-control", className)}
        {...fieldAria(inputId, { description, error })}
        {...rest}
      />
    </FieldShell>
  );
});
