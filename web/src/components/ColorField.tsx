// A colour setting (Story 27.11, the channel strip): the browser's colour
// picker next to a #RRGGBB text field, both editing the same value. An
// invalid text keeps the picker on the last good colour and shows why.
import { useEffect, useId, useState } from "react";

export const HEX_COLOR = /^#[0-9a-f]{6}$/i;

export function isHexColor(v: unknown): v is string {
  return typeof v === "string" && HEX_COLOR.test(v);
}

export interface ColorFieldProps {
  label?: string;
  value: string;
  onChange: (hex: string) => void;
  /** shown under the field (overrides the built-in "#RRGGBB" message) */
  error?: string;
  /** shown while the text is no colour; "": nothing (the caller shows it) */
  invalidText?: string;
  readOnly?: boolean;
  /** when the label is elsewhere (Settings rows) */
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  testId?: string;
}

export function ColorField({
  label,
  value,
  onChange,
  error,
  invalidText = "#RRGGBB",
  readOnly,
  testId,
  ...aria
}: ColorFieldProps) {
  const id = useId();
  const [text, setText] = useState(value);
  useEffect(() => setText(value), [value]);
  const bad = !isHexColor(text);
  const message = error ?? (bad && invalidText ? invalidText : undefined);
  const swatch = isHexColor(text) ? text.toLowerCase() : isHexColor(value) ? value : "#000000";
  return (
    <div className="mkt-color" data-testid={testId}>
      {label && (
        <label className="mkt-color__label" htmlFor={`${id}-hex`}>
          {label}
        </label>
      )}
      <div className="mkt-color__row">
        <input
          type="color"
          className="mkt-color__swatch"
          value={swatch}
          disabled={readOnly}
          aria-label={label ? `${label} (picker)` : undefined}
          aria-labelledby={label ? undefined : aria["aria-labelledby"]}
          onChange={(e) => {
            setText(e.target.value);
            onChange(e.target.value);
          }}
        />
        <input
          id={`${id}-hex`}
          type="text"
          dir="ltr"
          className="mkt-color__hex"
          value={text}
          readOnly={readOnly}
          spellCheck={false}
          maxLength={7}
          aria-invalid={bad || Boolean(error) || undefined}
          aria-labelledby={label ? undefined : aria["aria-labelledby"]}
          aria-describedby={aria["aria-describedby"]}
          onChange={(e) => {
            const v = e.target.value.trim();
            setText(v);
            onChange(isHexColor(v) ? v.toLowerCase() : v);
          }}
        />
      </div>
      {message && (
        <span className="mkt-color__err" role="alert">
          {message}
        </span>
      )}
    </div>
  );
}
