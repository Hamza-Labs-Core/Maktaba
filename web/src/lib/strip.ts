// The channel strip (Story 27.11): the settings a channel ends up with and
// what its bar shows, as the streaming engine draws it. Pure helpers, so the
// editor's preview and its tests agree with the engine on direction, clock
// rotation, 12/24 h and digits.
import type { RuleSection } from "./rulesApi";
import type { ChannelStrip, StripClock, StripSettings } from "./channels";

/** The built-in defaults (pipeline TvStripConfig), used until the rules load. */
export const DEFAULT_STRIP: StripSettings = {
  enabled: false,
  position: "bottom",
  height_pct: 9,
  bg_color: "#0b1f3a",
  bg_opacity: 0.85,
  accent_color: "#c8a24a",
  text_color: "#ffffff",
  font_size_pct: 42,
  direction: "auto",
  show_next: true,
  next_label_ar: "التالي",
  next_label_en: "Next",
  filler_label: "",
  overflow: "truncate",
  scroll_speed: 90,
  show_mode: "always",
  show_seconds: 20,
  every_minutes: 10,
  show_clock: true,
  clocks: [
    { zone: "Asia/Riyadh", label_ar: "مكة المكرمة", label_en: "Makkah" },
    { zone: "Africa/Cairo", label_ar: "القاهرة", label_en: "Cairo" },
    { zone: "UTC", label_ar: "غرينتش", label_en: "GMT" },
    { zone: "America/New_York", label_ar: "نيويورك", label_en: "New York" },
  ],
  clock_seconds: 8,
  clock_format: "24h",
  digits: "auto",
  show_channel_name: false,
};

const KEYS = Object.keys(DEFAULT_STRIP) as (keyof StripSettings)[];

/** The global strip settings: the rules section tv_strip's values over the defaults. */
export function stripDefaultsFromRules(sections: RuleSection[] | undefined): StripSettings {
  const sec = sections?.find((s) => s.key === "tv_strip");
  const out: Record<string, unknown> = { ...DEFAULT_STRIP };
  for (const f of sec?.fields ?? []) {
    if ((KEYS as string[]).includes(f.name) && f.value !== undefined && f.value !== null) {
      out[f.name] = f.value;
    }
  }
  return out as unknown as StripSettings;
}

/** A channel's own settings laid over the global ones. */
export function effectiveStrip(global: StripSettings, own?: ChannelStrip | null): StripSettings {
  const out: Record<string, unknown> = { ...global };
  for (const [k, v] of Object.entries(own ?? {})) {
    if ((KEYS as string[]).includes(k) && v !== undefined && v !== null) out[k] = v;
  }
  return out as unknown as StripSettings;
}

const ARABIC = /[؀-ۿ]/;

export function hasArabic(text: string | null | undefined): boolean {
  return ARABIC.test(text ?? "");
}

/** Right to left when set so, or ("auto") when the titles are Arabic. */
export function stripDirection(
  s: StripSettings,
  texts: Array<string | null | undefined>
): "rtl" | "ltr" {
  if (s.direction === "rtl" || s.direction === "ltr") return s.direction;
  return texts.some((t) => hasArabic(t)) ? "rtl" : "ltr";
}

export function arabicDigits(s: StripSettings, dir: "rtl" | "ltr"): boolean {
  return s.digits === "arabic" || (s.digits === "auto" && dir === "rtl");
}

const INDIC = "٠١٢٣٤٥٦٧٨٩";

export function toArabicDigits(text: string): string {
  return text.replace(/[0-9]/g, (d) => INDIC[Number(d)]);
}

/** Which clock shows at `nowMs`: each one for clock_seconds, in turn. */
export function clockIndex(count: number, clockSeconds: number, nowMs: number): number {
  if (count <= 0) return -1;
  const per = Math.max(1, clockSeconds) * 1000;
  return Math.floor(nowMs / per) % count;
}

/** The wall time in `zone` (IANA: daylight saving follows it), as the strip prints it. */
export function formatClock(
  at: Date,
  zone: string,
  format: StripSettings["clock_format"],
  indic: boolean
): string {
  let text: string;
  try {
    const parts = new Intl.DateTimeFormat("en-US", {
      timeZone: zone,
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: format === "12h" ? "h12" : "h23",
    }).formatToParts(at);
    const get = (type: string) => parts.find((p) => p.type === type)?.value ?? "";
    text = `${get("hour")}:${get("minute")}`;
    if (format === "12h") {
      const pm = get("dayPeriod").toUpperCase().startsWith("P");
      text += indic ? (pm ? " م" : " ص") : pm ? " PM" : " AM";
    }
  } catch {
    return "--:--";
  }
  return indic ? toArabicDigits(text) : text;
}

export function clockLabel(c: StripClock, dir: "rtl" | "ltr"): string {
  return (dir === "rtl" ? c.label_ar || c.label_en : c.label_en || c.label_ar) || c.zone;
}

export function nextLabel(s: StripSettings, dir: "rtl" | "ltr"): string {
  return dir === "rtl" ? s.next_label_ar || s.next_label_en : s.next_label_en || s.next_label_ar;
}

/** The fields of `draft` that differ from `base`: what a channel saves as its own. */
export function stripChanges(base: StripSettings, draft: StripSettings): ChannelStrip {
  const out: Record<string, unknown> = {};
  for (const k of KEYS) {
    if (JSON.stringify(base[k]) !== JSON.stringify(draft[k])) out[k] = draft[k];
  }
  return out as ChannelStrip;
}
