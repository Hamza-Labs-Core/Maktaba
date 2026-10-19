// What the rules page reads off a setting's descriptor (GET /api/rules):
// which control edits it, its unit, a slider range, whether a value is
// valid, and how a long group splits into sub-groups. Everything here is
// derived from the descriptor (kind, name, label, default, minimum,
// maximum, choices, columns) — no setting is special-cased by name.
import type { RuleField } from "../../lib/rulesApi";

export const WEEKDAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] as const;

export const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
export const baseKind = (kind: string) => kind.replace("?", "");
export const isOptional = (kind: string) => kind.endsWith("?");
export const asArray = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

/** A localised problem: an i18n key and its variables. */
export interface Problem {
  key: string;
  vars?: Record<string, string | number>;
}

export type Control =
  | "bool"
  | "select"
  | "weekday"
  | "number"
  | "time"
  | "text"
  | "url"
  | "days"
  | "chips"
  | "lines"
  | "list"
  | "table"
  | "none";

const TIME_TEXT = /^\d{1,2}:\d{2}$/;
const TIME_NAME = /(^|_)(time|start|end|from|until)$/;
const URL_NAME = /(^|_)(url|uri)$/;
const DAYS_NAME = /(^|_)days?$/;
const URL_VALUE = /^[a-z][a-z0-9+.-]*:\/\/[^\s/?#]+\S*$/i;

const isWeekday = (x: unknown) =>
  typeof x === "string" && (WEEKDAYS as readonly string[]).includes(x);

/** HH:MM between 00:00 and 24:00 (what the API accepts). */
export function validTime(s: string): boolean {
  const m = /^(\d{2}):(\d{2})$/.exec(s);
  if (!m) return false;
  const hh = Number(m[1]);
  const mm = Number(m[2]);
  return mm <= 59 && (hh < 24 || (hh === 24 && mm === 0));
}

export const validUrl = (s: string) => URL_VALUE.test(s.trim());

/** A text setting that holds a time of day: an HH:MM default, or a time-like name. */
function looksTime(name: string, samples: unknown[]): boolean {
  const texts = samples.filter((x) => typeof x === "string" && x !== "") as string[];
  if (texts.length) return texts.every((x) => TIME_TEXT.test(x));
  return TIME_NAME.test(name);
}

/** A list of weekday names (mon … sun) under a day-like name. */
function looksDays(name: string, samples: unknown[]): boolean {
  if (!DAYS_NAME.test(name)) return false;
  return samples.every((v) => asArray(v).every(isWeekday));
}

const edgeSpace = (samples: unknown[]) =>
  samples.some((v) => asArray(v).some((x) => typeof x === "string" && x !== x.trim()));

export function controlOf(f: RuleField): Control {
  const k = baseKind(f.kind);
  if (k === "bool") return "bool";
  if (f.choices?.length) return f.choices.every(isWeekday) ? "weekday" : "select";
  if (k === "int" || k === "float") return "number";
  if (k === "time") return "time";
  if (k === "str") {
    if (looksTime(f.name, [f.default, f.value])) return "time";
    return URL_NAME.test(f.name) ? "url" : "text";
  }
  if (k === "list" || k === "words") {
    const samples = [f.default, f.value];
    if (looksDays(f.name, samples)) return "days";
    // a phrase list where a trailing space means something is edited as text
    return edgeSpace(samples) ? "lines" : "chips";
  }
  if (k === "map" || k === "rows") return "table";
  return "none";
}

/** The control of a table column, from its kind, name and the values seen in it. */
export function columnControl(name: string, kind: string, samples: unknown[]): Control {
  const k = baseKind(kind);
  if (k === "bool") return "bool";
  if (k === "int" || k === "float") return "number";
  if (k === "time") return "time";
  if (k === "list" || k === "words") return looksDays(name, samples) ? "days" : "list";
  if (k === "str") {
    if (samples.some((x) => typeof x === "string" && x !== "") && looksTime(name, samples))
      return "time";
    return URL_NAME.test(name) ? "url" : "text";
  }
  return "text";
}

// ---- units -------------------------------------------------------------

export type Unit = "ms" | "s" | "min" | "h" | "d";

const UNIT_SUFFIX: [RegExp, Unit][] = [
  [/(^|_)(ms|millis|milliseconds)$/, "ms"],
  [/(^|_)(s|sec|secs|seconds)$/, "s"],
  [/(^|_)(min|mins|minutes)$/, "min"],
  [/(^|_)(h|hours)$/, "h"],
  [/(^|_)days$/, "d"],
];

/** The unit a number's name ends in (`_ms`, `_s`, `_minutes`, `_hours`, `_days`). */
export function unitOf(name: string): Unit | null {
  for (const [re, u] of UNIT_SUFFIX) if (re.test(name)) return u;
  return null;
}

const UNIT_SECONDS: Record<Unit, number> = { ms: 0.001, s: 1, min: 60, h: 3600, d: 86400 };

/** The value in the largest unit it fills (86400000 ms → 1 d), or null if that is its own unit. */
export function friendlyDuration(v: unknown, unit: Unit | null): { n: number; unit: Unit } | null {
  if (!unit || typeof v !== "number" || !Number.isFinite(v) || v === 0) return null;
  const secs = Math.abs(v) * UNIT_SECONDS[unit];
  const order: Unit[] = ["d", "h", "min", "s", "ms"];
  const best = order.find((u) => secs >= UNIT_SECONDS[u]) ?? "ms";
  if (best === unit || UNIT_SECONDS[best] < UNIT_SECONDS[unit]) return null;
  const n = Math.round((v * UNIT_SECONDS[unit] * 100) / UNIT_SECONDS[best]) / 100;
  return { n, unit: best };
}

// ---- slider ------------------------------------------------------------

export interface Range {
  min: number;
  max: number;
  step: number;
  /** the bounds come from the descriptor (out of range is an error) */
  strict: boolean;
}

function niceCeil(x: number): number {
  if (x <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(x));
  return Math.ceil(x / p) * p;
}

/**
 * A slider's range: the descriptor's minimum and maximum when both are
 * known; else, for a weight (relative, open-ended) a range around its
 * default, and for a 0–1 score or share the unit interval. Null: no slider.
 */
export function sliderRange(f: RuleField, value: unknown): Range | null {
  const k = baseKind(f.kind);
  if (k !== "int" && k !== "float") return null;
  const int = k === "int";
  const stepFor = (lo: number, hi: number) => (int ? 1 : niceCeil((hi - lo) / 100));
  if (f.minimum != null && f.maximum != null && f.maximum > f.minimum) {
    return { min: f.minimum, max: f.maximum, step: stepFor(f.minimum, f.maximum), strict: true };
  }
  const nums = [f.default, f.value, value].filter((x): x is number => typeof x === "number");
  const lo = f.minimum ?? 0;
  if (/(^|_)weights?(_|$)/.test(f.name)) {
    const hi = niceCeil(Math.max(1, ...nums) * 2);
    return { min: lo, max: hi, step: stepFor(lo, hi), strict: false };
  }
  if (
    !int &&
    /(^|_)(score|share|ratio|fraction)$/.test(f.name) &&
    nums.every((x) => x >= 0 && x <= 1)
  ) {
    return { min: Math.max(0, lo), max: 1, step: 0.05, strict: false };
  }
  return null;
}

// ---- validation --------------------------------------------------------

function numberProblem(
  v: unknown,
  kind: string,
  optional: boolean,
  minimum?: number,
  maximum?: number
): Problem | null {
  if (v === null || v === undefined || v === "") {
    return optional ? null : { key: "settings.rules.err.required" };
  }
  if (typeof v !== "number" || !Number.isFinite(v)) return { key: "settings.rules.err.number" };
  if (baseKind(kind) === "int" && !Number.isInteger(v)) return { key: "settings.rules.err.whole" };
  if (minimum != null && v < minimum)
    return { key: "settings.rules.err.min", vars: { min: minimum } };
  if (maximum != null && v > maximum)
    return { key: "settings.rules.err.max", vars: { max: maximum } };
  return null;
}

function timeProblem(v: unknown, optional: boolean): Problem | null {
  if (v === null || v === undefined || v === "") {
    return optional ? null : { key: "settings.rules.err.required" };
  }
  return typeof v === "string" && validTime(v) ? null : { key: "settings.rules.err.time" };
}

/** What is wrong with a value of a single (non-table) setting, or null. */
export function valueProblem(f: RuleField, v: unknown): Problem | null {
  const control = controlOf(f);
  const opt = isOptional(f.kind);
  switch (control) {
    case "number":
      return numberProblem(v, f.kind, opt, f.minimum, f.maximum);
    case "time":
      // a text setting that may be empty by default may be left empty
      return timeProblem(v, opt || f.default === "" || f.default == null);
    case "url":
      return typeof v === "string" && v.trim() !== "" && !validUrl(v)
        ? { key: "settings.rules.err.url" }
        : null;
    case "select":
    case "weekday":
      return v == null || f.choices?.includes(String(v))
        ? null
        : { key: "settings.rules.err.choice" };
    default:
      return null;
  }
}

export interface Column {
  name: string;
  label: string;
  kind: string;
  optional: boolean;
  control: Control;
  unit: Unit | null;
}

/** Each cell's problem, keyed "row:col" (indexes from 0). */
export function cellProblems(cols: Column[], rows: unknown): Record<string, Problem> {
  const out: Record<string, Problem> = {};
  asArray(rows).forEach((r, i) => {
    cols.forEach((c, j) => {
      const present = Array.isArray(r)
        ? j < r.length
        : !!r && typeof r === "object" && c.name in (r as object);
      const v = Array.isArray(r)
        ? r[j]
        : r && typeof r === "object"
          ? (r as Record<string, unknown>)[c.name]
          : undefined;
      if (!present && c.optional) return;
      const optional = c.optional || isOptional(c.kind);
      let p: Problem | null = null;
      if (c.control === "number") p = numberProblem(v, c.kind, optional);
      else if (c.control === "time") p = timeProblem(v, optional);
      else if (c.control === "url" && typeof v === "string" && v.trim() !== "" && !validUrl(v))
        p = { key: "settings.rules.err.url" };
      if (p) out[`${i}:${j}`] = p;
    });
  });
  return out;
}

/** A start/end pair whose end is earlier than its start (the window runs past midnight). */
export function wrapsMidnight(start: unknown, end: unknown): boolean {
  if (typeof start !== "string" || typeof end !== "string") return false;
  if (!validTime(start) || !validTime(end) || end === "00:00" || end === "24:00") return false;
  return end < start;
}

// ---- sub-groups --------------------------------------------------------

export interface SubGroup {
  /** null: the group's own settings, shown first without a heading */
  title: string | null;
  fields: RuleField[];
}

const humanise = (s: string) => {
  const t = s.replace(/_/g, " ");
  return t.charAt(0).toUpperCase() + t.slice(1);
};

/** The words every label starts with ("Filler weight: …" → "Filler weight"). */
function commonLabel(labels: string[]): string {
  if (!labels.length) return "";
  let p = labels[0];
  for (const l of labels.slice(1)) {
    let i = 0;
    while (i < p.length && i < l.length && p[i] === l[i]) i++;
    p = p.slice(0, i);
  }
  // keep a whole word: one label must end its word where the prefix ends
  const whole = labels.some((l) => l.length === p.length || /[\s:(,–-]/.test(l[p.length]));
  if (!whole) p = p.slice(0, Math.max(0, p.lastIndexOf(" ")));
  return p.replace(/[\s:(,–-]+$/, "").trim();
}

/**
 * Splits a long group into sub-groups by the settings' common name
 * prefix (`break_…`, `mix_…`, `dz_weight_…`). A prefix most of the group
 * shares (`dz_`) is a namespace and is skipped first. A family needs at
 * least `minFamily` settings; the rest stay in the group itself.
 */
export function subGroups(fields: RuleField[], minGroup = 8, minFamily = 3): SubGroup[] {
  if (fields.length < minGroup) return [{ title: null, fields }];
  const tokens = fields.map((f) => f.name.split("_").filter(Boolean));
  const firsts = new Map<string, number>();
  for (const t of tokens) firsts.set(t[0], (firsts.get(t[0]) ?? 0) + 1);
  const ns = [...firsts].find(([, n]) => n >= Math.max(minFamily, fields.length * 0.6))?.[0];
  const stems = tokens.map((t) => (ns && t[0] === ns && t.length > 1 ? t.slice(1) : t));
  const count = new Map<string, number>();
  for (const s of stems) if (s.length > 1) count.set(s[0], (count.get(s[0]) ?? 0) + 1);
  const families = new Map<string, RuleField[]>();
  const own: RuleField[] = [];
  fields.forEach((f, i) => {
    const s = stems[i];
    const fam = s.length > 1 && (count.get(s[0]) ?? 0) >= minFamily ? s[0] : null;
    if (!fam) own.push(f);
    else families.set(fam, [...(families.get(fam) ?? []), f]);
  });
  if (families.size === 0) return [{ title: null, fields }];
  const out: SubGroup[] = own.length ? [{ title: null, fields: own }] : [];
  for (const [fam, fs] of families) {
    const label = commonLabel(fs.map((f) => f.label));
    out.push({ title: label.length >= 3 ? label : humanise(fam), fields: fs });
  }
  return out;
}

/** Localised short and long weekday names, Monday first. */
export function weekdayNames(locale: string): { short: string; long: string }[] {
  return WEEKDAYS.map((_, i) => {
    // 2024-01-01 is a Monday
    const d = new Date(Date.UTC(2024, 0, 1 + i));
    const fmt = (weekday: "short" | "long") => {
      try {
        return new Intl.DateTimeFormat(locale, { weekday, timeZone: "UTC" }).format(d);
      } catch {
        return WEEKDAYS[i];
      }
    };
    return { short: fmt("short"), long: fmt("long") };
  });
}
