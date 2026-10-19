// The strip's colours (Story 27.11): a palette to start from, then each
// colour with the browser's picker and its #RRGGBB text, the background's
// opacity as a slider beside it, and "reset to default". Used by the channel
// editor's Strip section and Settings → Channel strip.
import { Button } from "@ds/components/Button/Button";
import { ColorField } from "../../../components/ColorField";
import { useI18n } from "../../../lib/i18n";
import { STRIP_COLOR_KEYS, STRIP_PALETTES, type StripColors } from "../../../lib/strip";

const FIELDS = [
  ["bg_color", "channels.strip.bgColor"],
  ["accent_color", "channels.strip.accentColor"],
  ["text_color", "channels.strip.textColor"],
  ["next_color", "channels.strip.nextColor"],
  ["clock_text_color", "channels.strip.clockTextColor"],
  ["indicator_color", "channels.strip.indicatorColor"],
] as const;

export function sameColors(a: StripColors, b: StripColors): boolean {
  return STRIP_COLOR_KEYS.every((k) =>
    typeof a[k] === "string"
      ? String(a[k]).toLowerCase() === String(b[k]).toLowerCase()
      : Number(a[k]) === Number(b[k])
  );
}

export function StripColorsEditor({
  value,
  defaults,
  errors = {},
  onChange,
  readOnly,
}: {
  value: StripColors;
  /** what "reset to default" goes back to */
  defaults: StripColors;
  errors?: Partial<Record<string, string>>;
  onChange: (c: Partial<StripColors>) => void;
  readOnly?: boolean;
}) {
  const { t } = useI18n();
  return (
    <div className="mkt-strip__clocks" data-testid="strip-colors">
      <span className="mkt-art__name">{t("channels.strip.colors")}</span>
      <div className="mkt-strip__palettes" role="group" aria-label={t("channels.strip.palettes")}>
        {STRIP_PALETTES.map((p) => {
          const on = sameColors(value, p.colors);
          return (
            <button
              key={p.id}
              type="button"
              className={`mkt-strip__palette${on ? " mkt-strip__palette--on" : ""}`}
              aria-pressed={on}
              disabled={readOnly}
              onClick={() => onChange({ ...p.colors })}
            >
              <span className="mkt-strip__dot" style={{ background: p.colors.bg_color }} />
              <span className="mkt-strip__dot" style={{ background: p.colors.accent_color }} />
              {t(`channels.strip.palette.${p.id}`)}
            </button>
          );
        })}
        <Button
          size="sm"
          variant="ghost"
          disabled={readOnly || sameColors(value, defaults)}
          onClick={() => onChange({ ...defaults })}
        >
          {t("channels.strip.resetColors")}
        </Button>
      </div>
      <div className="mkt-strip__grid">
        {FIELDS.map(([k, label]) => (
          <div key={k} className={k === "bg_color" ? "mkt-strip__clocks" : undefined}>
            <ColorField
              label={t(label)}
              testId={`color-${k}`}
              value={value[k]}
              readOnly={readOnly}
              error={errors[k]}
              invalidText={t("channels.strip.err.color")}
              onChange={(v) => onChange({ [k]: v })}
            />
            {k === "bg_color" && (
              <label className="mkt-strip__opacity">
                <span>
                  {t("channels.strip.bgOpacity")}: {Math.round(value.bg_opacity * 100)}%
                </span>
                <input
                  type="range"
                  min={0}
                  max={1}
                  step={0.05}
                  value={value.bg_opacity}
                  disabled={readOnly}
                  onChange={(e) => onChange({ bg_opacity: Number(e.target.value) })}
                />
              </label>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
