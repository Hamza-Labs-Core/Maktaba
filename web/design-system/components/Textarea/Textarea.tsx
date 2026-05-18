import { forwardRef } from "react";
import type { TextareaHTMLAttributes, ReactNode } from "react";
import { clsx } from "../internal/clsx";
import { useFieldId } from "../internal/useId";
import { FieldShell, fieldAria } from "../Field/Field";
import "../Field/field.css";

// Story 17.2 — Textarea. Same FieldShell contract as Input; defaults to
// a vertical resize so long-form copy (subtitle notes, descriptions)
// can grow without clipping.

export interface TextareaProps
  extends Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "id"> {
  id?: string;
  label?: ReactNode;
  description?: ReactNode;
  error?: ReactNode;
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { id, label, description, error, required, className, rows = 4, ...rest },
  ref
) {
  const areaId = useFieldId(id);
  return (
    <FieldShell
      htmlFor={areaId}
      label={label}
      description={description}
      error={error}
      required={required}
    >
      <textarea
        ref={ref}
        id={areaId}
        rows={rows}
        required={required}
        className={clsx("mk-control mk-textarea", className)}
        {...fieldAria(areaId, { description, error })}
        {...rest}
      />
    </FieldShell>
  );
});
