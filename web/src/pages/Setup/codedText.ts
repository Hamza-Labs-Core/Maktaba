// Coded messages in the screen's language (Epic 38 stories 38.4 and 38.6): the
// setup steps and the server name what they say by a code and its values, and
// the app has the words (`<prefix>.<code>` in src/i18n/en.json and ar.json).
// The server's English is the fallback of a step's message, or goes under
// "Details".
import { formatBytes } from "../../lib/desktopSetup";

export type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** The text of a code (`${prefix}.${code}`), its params in it (sizes
 * formatted) with the platform's words; `fallback` when the app has no text
 * for the code. */
export function codedText(
  t: Translate,
  prefix: string,
  code: string,
  params: Record<string, unknown> | undefined,
  words: Record<string, string>,
  fallback: string
): string {
  const key = `${prefix}.${code}`;
  const vars: Record<string, string | number> = { ...words };
  for (const [k, v] of Object.entries(params ?? {})) {
    if (typeof v === "number") vars[k] = k.endsWith("_bytes") ? formatBytes(v) : v;
    else if (v !== null && v !== undefined) vars[k] = String(v);
  }
  const text = t(key, vars);
  return text === key ? fallback : text;
}
