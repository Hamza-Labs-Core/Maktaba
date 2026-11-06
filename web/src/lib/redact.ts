// The shared redaction list (shared/redact/rules.json, Epic 53 story 53.01): the
// same rules every Maktaba logger uses, and the same cases they pass
// (shared/redact/cases.json). redactRules.json is the copy tools/log-schema/sync.py
// writes. A replacement names its groups as $1, which String.replace reads as is.
import raw from "./redactRules.json";

interface ValueRule {
  name: string;
  pattern: string;
  ignore_case?: boolean;
  replace: string;
  needs?: string[];
}

interface Compiled {
  re: RegExp;
  replace: string;
  needs: string[];
  ignoreCase: boolean;
}

const rules = raw as unknown as {
  key_mask: string;
  value_mask: string;
  keys: string[];
  key_suffixes: string[];
  key_parts_string_only: string[];
  values: ValueRule[];
  cards: {
    pattern: string;
    first_digits: string;
    min_digits: number;
    max_digits: number;
    replace: string;
  };
  ipv4: { pattern: string; keep_first_octet: string[] };
  ipv6: { pattern: string };
};

export const KEY_MASK = rules.key_mask;
export const VALUE_MASK = rules.value_mask;

const keys = new Set(rules.keys.map((k) => k.toLowerCase()));
const values: Compiled[] = rules.values.map((v) => ({
  re: new RegExp(v.pattern, v.ignore_case ? "gi" : "g"),
  replace: v.replace,
  needs: (v.needs ?? []).map((n) => (v.ignore_case ? n.toLowerCase() : n)),
  ignoreCase: !!v.ignore_case,
}));
const cards = new RegExp(rules.cards.pattern, "g");
const ipv4 = new RegExp(rules.ipv4.pattern, "g");
const ipv6 = new RegExp(rules.ipv6.pattern, "g");

/** Whether a field named `key` is masked whole; `isString` says whether its value is a string. */
export function keySensitive(key: string, isString: boolean): boolean {
  const low = key.toLowerCase();
  if (keys.has(low) || rules.key_suffixes.some((s) => low.endsWith(s))) return true;
  return isString && rules.key_parts_string_only.some((p) => low.includes(p));
}

const isDigit = (c: string) => c >= "0" && c <= "9";
const isHex = (c: string) => c !== "" && "0123456789abcdefABCDEF".includes(c);

function replaceChecked(
  text: string,
  re: RegExp,
  fn: (m: string, before: string, after: string) => string
): string {
  return text.replace(re, (m: string, ...rest: unknown[]) => {
    // the offset is the first number after the groups
    const at = rest.find((r) => typeof r === "number") as number;
    return fn(m, at > 0 ? text[at - 1] : "", text[at + m.length] ?? "");
  });
}

function luhn(d: string[]): boolean {
  let sum = 0;
  for (let i = 0; i < d.length; i++) {
    let n = Number(d[d.length - 1 - i]);
    if (i % 2) {
      n *= 2;
      if (n > 9) n -= 9;
    }
    sum += n;
  }
  return sum % 10 === 0;
}

function card(m: string, before: string, after: string): string {
  if (isDigit(before) || isDigit(after)) return m;
  const d = [...m].filter(isDigit);
  const c = rules.cards;
  if (d.length < c.min_digits || d.length > c.max_digits || !c.first_digits.includes(d[0]))
    return m;
  return luhn(d) ? c.replace : m;
}

function v4(m: string, before: string, after: string): string {
  // part of a longer number (a version like 1.2.3.4.5 is cut from its start only)
  if (isDigit(before) || before === "." || isDigit(after)) return m;
  const p = m.split(".");
  if (p.some((x) => Number(x) > 255) || rules.ipv4.keep_first_octet.includes(p[0])) return m;
  return `${p[0]}.${p[1]}.${p[2]}.0`;
}

function ipv6Groups(m: string): string[] | null {
  if (m.includes(":::") || m.split("::").length > 2) return null;
  let groups: string[];
  const at = m.indexOf("::");
  if (at >= 0) {
    const left = m.slice(0, at) ? m.slice(0, at).split(":") : [];
    const right = m.slice(at + 2) ? m.slice(at + 2).split(":") : [];
    if (left.length + right.length > 7) return null;
    groups = [...left, ...Array<string>(8 - left.length - right.length).fill("0"), ...right];
  } else {
    groups = m.split(":");
    if (groups.length !== 8) return null;
  }
  return groups.some((g) => g === "") ? null : groups;
}

function v6(m: string, before: string, after: string): string {
  if (isHex(before) || before === ":" || isHex(after) || after === ":") return m;
  const g = ipv6Groups(m);
  if (!g) return m;
  if (g.slice(0, 7).every((x) => parseInt(x, 16) === 0) && parseInt(g[7], 16) === 1) return m;
  return `${g[0]}:${g[1]}:${g[2]}::`;
}

/** `text` with the shared value rules applied: credentials, emails, home folders, cards, IPs. */
export function scrub(text: string): string {
  if (!text) return text;
  let out = text;
  let low = out.toLowerCase();
  for (const v of values) {
    const hay = v.ignoreCase ? low : out;
    if (v.needs.length && !v.needs.some((n) => hay.includes(n))) continue;
    const next = out.replace(v.re, v.replace);
    if (next !== out) {
      out = next;
      low = out.toLowerCase();
    }
  }
  let digits = 0;
  let dots = 0;
  let colons = 0;
  for (const c of out) {
    if (isDigit(c)) digits++;
    else if (c === ".") dots++;
    else if (c === ":") colons++;
  }
  if (digits >= rules.cards.min_digits) out = replaceChecked(out, cards, card);
  if (dots >= 3 && digits >= 4) out = replaceChecked(out, ipv4, v4);
  if (colons >= 3) out = replaceChecked(out, ipv6, v6);
  return out;
}

/** A flat attribute map with masked keys and scrubbed strings. */
export function redactAttrs(attrs: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(attrs)) {
    if (keySensitive(k, typeof v === "string")) out[k] = KEY_MASK;
    else out[k] = typeof v === "string" ? scrub(v) : v;
  }
  return out;
}
