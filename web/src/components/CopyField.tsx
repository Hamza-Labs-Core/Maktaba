// A labelled address with a Copy button that also works on plain-http LAN
// origins (lib/clipboard.ts). The text is always shown in a read-only field,
// so it can be selected and copied by hand; a click in it selects it all.
import { useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";
import { copyText, selectField } from "../lib/clipboard";

export function CopyField({
  label,
  value,
  className,
}: {
  label: string;
  value: string;
  className?: string;
}) {
  const { t } = useI18n();
  const field = useRef<HTMLInputElement>(null);
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");

  async function copy() {
    const ok = await copyText(value, field.current);
    if (!ok) selectField(field.current); // left selected: Ctrl+C copies it
    setState(ok ? "copied" : "failed");
  }

  return (
    <div className={["mkt-copy", className].filter(Boolean).join(" ")}>
      <span className="mkt-copy__label">{label}</span>
      <input
        ref={field}
        className="mkt-copy__value"
        readOnly
        value={value}
        aria-label={label}
        dir="ltr"
        spellCheck={false}
        onFocus={(e) => selectField(e.currentTarget)}
        onClick={(e) => selectField(e.currentTarget)}
      />
      <Button size="sm" variant="secondary" onClick={copy} aria-label={t("livetv.copy", { label })}>
        {state === "copied"
          ? t("livetv.copied")
          : state === "failed"
            ? t("livetv.copyFailed")
            : t("livetv.copyBtn")}
      </Button>
      {state === "failed" && (
        <span role="status" className="mkt-copy__hint">
          {t("livetv.copyManual")}
        </span>
      )}
    </div>
  );
}
