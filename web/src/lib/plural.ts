// A plural string: `<base>.<category>` by the locale's plural rules (Arabic has six:
// zero, one, two, few, many, other), falling back to `<base>.other`.
import { lookupMessage } from "./i18n";

type Locale = "en" | "ar";

export function pluralKey(locale: Locale, base: string, count: number): string {
  const cat = new Intl.PluralRules(locale).select(count);
  const key = `${base}.${cat}`;
  return lookupMessage(locale, key) !== undefined ? key : `${base}.other`;
}
