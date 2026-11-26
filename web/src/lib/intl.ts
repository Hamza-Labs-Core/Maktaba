// Intl formatters kept by locale and options. Making an Intl formatter costs far more
// than formatting with one, and pages format every number and date of every row on
// every render: typing in a long settings page made dozens per keystroke (#697).
const numberFormats = new Map<string, Intl.NumberFormat>();
const dateTimeFormats = new Map<string, Intl.DateTimeFormat>();
const relativeTimeFormats = new Map<string, Intl.RelativeTimeFormat>();

/**
 * BCP 47 tag for Intl formatters: Arabic uses Western digits (`-u-nu-latn`)
 * across every Maktaba client unless a caller explicitly sets another numbering system.
 */
export function intlLocale(tag: string): string {
  if (/^ar(?:[-_]|$)/i.test(tag) && !/-u-(?:.*-)?nu-/i.test(tag)) {
    const base = tag.replace(/_/g, "-");
    return base.includes("-u-") ? `${base}-nu-latn` : `${base}-u-nu-latn`;
  }
  return tag;
}

function keyOf(tag: string, opts: object | undefined): string {
  return `${tag}|${JSON.stringify(opts ?? null)}`;
}

/** `new Intl.NumberFormat(intlLocale(tag), opts)`, made once per tag and options. */
export function numberFormat(tag: string, opts?: Intl.NumberFormatOptions): Intl.NumberFormat {
  const resolved = intlLocale(tag);
  const key = keyOf(resolved, opts);
  let f = numberFormats.get(key);
  if (!f) {
    f = new Intl.NumberFormat(resolved, opts);
    numberFormats.set(key, f);
  }
  return f;
}

/** `new Intl.DateTimeFormat(intlLocale(tag), opts)`, made once per tag and options. */
export function dateTimeFormat(
  tag: string,
  opts?: Intl.DateTimeFormatOptions
): Intl.DateTimeFormat {
  const resolved = intlLocale(tag);
  const key = keyOf(resolved, opts);
  let f = dateTimeFormats.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(resolved, opts);
    dateTimeFormats.set(key, f);
  }
  return f;
}

/** `new Intl.RelativeTimeFormat(intlLocale(tag), opts)`, made once per tag and options. */
export function relativeTimeFormat(
  tag: string,
  opts?: Intl.RelativeTimeFormatOptions
): Intl.RelativeTimeFormat {
  const resolved = intlLocale(tag);
  const key = keyOf(resolved, opts);
  let f = relativeTimeFormats.get(key);
  if (!f) {
    f = new Intl.RelativeTimeFormat(resolved, opts);
    relativeTimeFormats.set(key, f);
  }
  return f;
}
