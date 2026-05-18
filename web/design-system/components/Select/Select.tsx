import { forwardRef } from "react";
import type { SelectHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import { FieldShell, fieldAria } from "../Field/Field";
import "../Field/field.css";

// Story 17.2 — Select. Native <select> (best a11y + mobile/TV behaviour)
// styled to the shared control surface. Options can be passed as
// children or via the `options` convenience prop.

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SelectProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, "id"> {
  id?: string;
  label?: ReactNode;
  description?: ReactNode;
  error?: ReactNode;
  options?: SelectOption[];
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select(
  { id, label, description, error, required, className, options, children, ...rest },
  ref
) {
  const selectId = useFieldId(id);
  return (
    <FieldShell
      htmlFor={selectId}
      label={label}
      description={description}
      error={error}
      required={required}
    >
      <select
        ref={ref}
        id={selectId}
        required={required}
        className={clsx("mk-control mk-select", className)}
        {...fieldAria(selectId, { description, error })}
        {...rest}
      >
        {options
          ? options.map((o) => (
              <option key={o.value} value={o.value} disabled={o.disabled}>
                {o.label}
              </option>
            ))
          : children}
      </select>
    </FieldShell>
  );
});
