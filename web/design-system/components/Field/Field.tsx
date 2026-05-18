import type { ReactNode } from "react";
import { clsx } from "../internal/clsx";
import "./field.css";

// Shared label/description/error scaffold for the form primitives
// (Input, Textarea, Select, Combobox). Centralising it keeps the a11y
// wiring — `aria-describedby`, `aria-invalid`, the error `role="alert"`
// — identical across every field instead of copy-pasted per control.

export interface FieldShellProps {
  /** Stable id of the control this field labels. */
  htmlFor: string;
  label?: ReactNode;
  /** Helper text rendered under the control; id = `${htmlFor}-desc`. */
  description?: ReactNode;
  /** Error message; when set the control is `aria-invalid`. id = `${htmlFor}-err`. */
  error?: ReactNode;
  required?: boolean;
  className?: string;
  children: ReactNode;
}

/**
 * Resolves the `aria-describedby` / `aria-invalid` a control should
 * receive given a description and/or error. Used by the field primitives
 * so the association is computed once, consistently.
 */
export function fieldAria(
  id: string,
  opts: { description?: unknown; error?: unknown }
): { "aria-describedby"?: string; "aria-invalid"?: true } {
  const ids: string[] = [];
  if (opts.description) ids.push(`${id}-desc`);
  if (opts.error) ids.push(`${id}-err`);
  return {
    "aria-describedby": ids.length ? ids.join(" ") : undefined,
    "aria-invalid": opts.error ? true : undefined,
  };
}

export function FieldShell({
  htmlFor,
  label,
  description,
  error,
  required,
  className,
  children,
}: FieldShellProps) {
  return (
    <div className={clsx("mk-field", error ? "mk-field--invalid" : null, className)}>
      {label != null && (
        <label className="mk-field__label" htmlFor={htmlFor}>
          {label}
          {required && (
            <span className="mk-field__required" aria-hidden="true">
              {" *"}
            </span>
          )}
        </label>
      )}
      {children}
      {description != null && !error && (
        <p className="mk-field__desc" id={`${htmlFor}-desc`}>
          {description}
        </p>
      )}
      {error != null && (
        <p className="mk-field__error" id={`${htmlFor}-err`} role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
