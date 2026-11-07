// Intl formatters kept by locale and options. Making an Intl formatter costs far more
// than formatting with one, and pages format every number and date of every row on
// every render: typing in a long settings page made dozens per keystroke (#697).
const numberFormats = new Map<string, Intl.NumberFormat>();
const dateTimeFormats = new Map<string, Intl.DateTimeFormat>();

function keyOf(tag: string, opts: object | undefined): string {
  return `${tag}|${JSON.stringify(opts ?? null)}`;
}

/** `new Intl.NumberFormat(tag, opts)`, made once per tag and options. */
export function numberFormat(tag: string, opts?: Intl.NumberFormatOptions): Intl.NumberFormat {
  const key = keyOf(tag, opts);
  let f = numberFormats.get(key);
  if (!f) {
    f = new Intl.NumberFormat(tag, opts);
    numberFormats.set(key, f);
  }
  return f;
}

/** `new Intl.DateTimeFormat(tag, opts)`, made once per tag and options. */
export function dateTimeFormat(
  tag: string,
  opts?: Intl.DateTimeFormatOptions
): Intl.DateTimeFormat {
  const key = keyOf(tag, opts);
  let f = dateTimeFormats.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(tag, opts);
    dateTimeFormats.set(key, f);
  }
  return f;
}
