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
  bg_color: "#0b5d3b",
  bg_opacity: 0.9,
  accent_color: "#c9a227",
  rule_height: 4,
  text_color: "#ffffff",
  next_color: "#e8d9a8",
  clock_text_color: "#0b3d27",
  indicator_color: "#eac85e",
  font_size_pct: 42,
  direction: "auto",
  show_next: true,
  next_label_ar: "التالي",
  next_label_en: "Next",
  filler_label: "",
  overflow: "cycle",
  page_seconds: 4,
  page_indicator: true,
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

/** The strip's colour settings (what a palette sets). */
export const STRIP_COLOR_KEYS = [
  "bg_color",
  "bg_opacity",
  "accent_color",
  "text_color",
  "next_color",
  "clock_text_color",
  "indicator_color",
] as const;
export type StripColors = Pick<StripSettings, (typeof STRIP_COLOR_KEYS)[number]>;

/** Ready-made palettes; the first is the default. */
export const STRIP_PALETTES: { id: string; colors: StripColors }[] = [
  {
    id: "green_gold",
    colors: {
      bg_color: "#0b5d3b",
      bg_opacity: 0.9,
      accent_color: "#c9a227",
      text_color: "#ffffff",
      next_color: "#e8d9a8",
      clock_text_color: "#0b3d27",
      indicator_color: "#eac85e",
    },
  },
  {
    id: "navy",
    colors: {
      bg_color: "#0b1f3a",
      bg_opacity: 0.85,
      accent_color: "#c8a24a",
      text_color: "#ffffff",
      next_color: "#d9e2f2",
      clock_text_color: "#0b1f3a",
      indicator_color: "#e6c76a",
    },
  },
  {
    id: "black",
    colors: {
      bg_color: "#000000",
      bg_opacity: 0.8,
      accent_color: "#e0b84c",
      text_color: "#ffffff",
      next_color: "#d0d0d0",
      clock_text_color: "#111111",
      indicator_color: "#e0b84c",
    },
  },
  {
    id: "maroon_gold",
    colors: {
      bg_color: "#5c0f1d",
      bg_opacity: 0.9,
      accent_color: "#c9a227",
      text_color: "#ffffff",
      next_color: "#f0dfb0",
      clock_text_color: "#3a0a12",
      indicator_color: "#e8c35a",
    },
  },
];

/** The colours of `s`, for comparing with a palette. */
export function stripColors(s: StripSettings): StripColors {
  const out: Record<string, unknown> = {};
  for (const k of STRIP_COLOR_KEYS) out[k] = s[k];
  return out as StripColors;
}

/** The accent rule's thickness in pixels for a bar `h` high (0: none). */
export function ruleThickness(h: number, rulePct: number): number {
  if (!(rulePct > 0)) return 0;
  return Math.max(1, Math.round((h * rulePct) / 100));
}

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

// ─── overflow "cycle": a long line in pages that take turns ───────────
// The same rules as the streaming engine: pages break at words and fit the
// title area with a 0.9 safety margin; the pages are balanced (the narrowest
// width that keeps their count); a page ending inside a bracket (﴿…﴾ {…}
// (…) «…») closes it and the next page reopens it; a verse reference goes on
// the last page (" · <ref>") when it fits, else on a page of its own.

/** Width of `text` in the strip's font (pixels of the output picture). */
export type Measure = (text: string) => number;

export const PAGE_MARGIN = 0.9;

const OPEN: Record<string, string> = { "﴿": "﴾", "{": "}", "(": ")", "«": "»" };
const CLOSE = new Set(Object.values(OPEN));

/** The brackets still open after `text`, given those open before it. */
function openAfter(open: string[], text: string): string[] {
  const st = [...open];
  for (const ch of text) {
    if (OPEN[ch]) st.push(ch);
    else if (CLOSE.has(ch) && st.length && OPEN[st[st.length - 1]] === ch) st.pop();
  }
  return st;
}

/** The page texts for words broken into groups, brackets kept per page. */
function render(groups: string[][]): string[] {
  const out: string[] = [];
  let open: string[] = [];
  for (const g of groups) {
    const body = g.join(" ");
    const after = openAfter(open, body);
    const text =
      open.join("") +
      body +
      [...after]
        .reverse()
        .map((o) => OPEN[o])
        .join("");
    out.push(text);
    open = after;
  }
  return out;
}

/** Greedy groups of words whose pages are at most `width` wide. */
function greedy(words: string[], width: number, measure: Measure): string[][] {
  const groups: string[][] = [];
  let cur: string[] = [];
  for (const w of words) {
    const tryGroups = [...groups, [...cur, w]];
    const pages = render(tryGroups);
    if (cur.length && measure(pages[pages.length - 1]) > width) {
      groups.push(cur);
      cur = [w];
    } else {
      cur.push(w);
    }
  }
  if (cur.length) groups.push(cur);
  return groups;
}

/** The pages of `text` for a title area `areaWidth` wide (see above). */
export function pageLine(
  text: string,
  areaWidth: number,
  measure: Measure,
  ref?: string | null
): string[] {
  const words = text.split(/\s+/).filter(Boolean);
  const max = areaWidth * PAGE_MARGIN;
  if (!words.length) return ref ? [ref] : [];
  let groups = greedy(words, max, measure);
  if (groups.length > 1) {
    // balance (as the engine): 12 halvings between limit/n and limit for the
    // narrowest width that keeps the n pages
    const n = groups.length;
    let lo = max / n;
    let hi = max;
    for (let i = 0; i < 12; i++) {
      const mid = (lo + hi) / 2;
      if (greedy(words, mid, measure).length <= n) hi = mid;
      else lo = mid;
    }
    groups = greedy(words, hi, measure);
  }
  const pages = render(groups);
  if (ref) {
    const last = `${pages[pages.length - 1]} · ${ref}`;
    if (measure(last) <= max) pages[pages.length - 1] = last;
    else pages.push(ref);
  }
  return pages;
}

/** How long a page shows: page_seconds × clamp(0.75 + 0.75 × fill, 1, 2). */
export function pageSeconds(pageWidth: number, areaWidth: number, base: number): number {
  const fill = areaWidth > 0 ? pageWidth / areaWidth : 1;
  return base * Math.min(2, Math.max(1, 0.75 + 0.75 * fill));
}

/** The «التالي» line pages more slowly. */
export const NEXT_PAGE_FACTOR = 1.5;

/** «١/٢» or «1/2» per the digits rule. */
export function pageIndicator(i: number, n: number, indic: boolean): string {
  const s = `${i + 1}/${n}`;
  return indic ? toArabicDigits(s) : s;
}

/** The engine's width estimate of one character, in ems (no font metrics at hand). */
export function runeEms(ch: string): number {
  const c = ch.codePointAt(0) ?? 0;
  if (ch === " ") return 0.28;
  if ((c >= 0x064b && c <= 0x065f) || c === 0x0670 || c === 0x200e || c === 0x200f) return 0;
  if (/\p{Script=Arabic}/u.test(ch) && /\p{L}/u.test(ch)) return 0.5;
  if (/\p{Nd}/u.test(ch)) return 0.56;
  if (/\p{Lu}/u.test(ch)) return 0.68;
  if (/\p{L}/u.test(ch)) return 0.52;
  if (/[\p{P}\p{S}]/u.test(ch)) return 0.36;
  return 0.55;
}

/** `text`'s estimated width at `fontPx` (the engine's estimate). */
export function estimateWidth(text: string, fontPx: number): number {
  let em = 0;
  for (const ch of text) em += runeEms(ch);
  return em * fontPx;
}

/** The strip's layout on a W×H frame, as the engine computes it (strip.go Layout). */
export function stripLayout(
  s: StripSettings,
  opts: {
    W?: number;
    H?: number;
    clock: boolean;
    hasNext: boolean;
    channelName?: string;
    measure?: (text: string, fontPx: number) => number;
  }
) {
  const W = opts.W ?? 1920;
  const H = opts.H ?? 1080;
  const measure = opts.measure ?? estimateWidth;
  let h = Math.round((H * s.height_pct) / 100);
  h -= h % 2;
  h = Math.max(16, h);
  const pad = Math.floor(h / 4);
  const clockW = opts.clock ? Math.floor((h * 19) / 10) : 0;
  const titlePx = Math.max(8, Math.floor((h * s.font_size_pct) / 100));
  const nextPx = Math.max(8, Math.floor((titlePx * 2) / 3));
  // the page number is in the «التالي» line's size
  const indicatorPx = nextPx;
  const indicatorW =
    s.overflow === "cycle" && s.page_indicator ? measure("٠٠/٠٠", indicatorPx) + pad : 0;
  const nameW =
    s.show_channel_name && opts.channelName
      ? Math.min(W / 4, [...opts.channelName].length * titlePx * 0.55 + 2 * pad)
      : 0;
  const areaWidth = W - clockW - 2 * pad - (nameW ? nameW + pad : 0) - indicatorW;
  return {
    h,
    pad,
    clockW,
    titlePx,
    nextPx,
    indicatorPx,
    indicatorW,
    nameW,
    areaWidth,
    clockLabelPx: Math.floor((h * 26) / 100),
    clockTimePx: Math.floor((h * 40) / 100),
  };
}
