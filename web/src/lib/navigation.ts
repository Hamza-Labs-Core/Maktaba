// Whole-page navigation and the addresses the app follows. Kept in one module so
// tests can mock it (jsdom's window.location cannot be spied on).

/** Reloads the page (a new build may be waiting). */
export function reloadPage(): void {
  window.location.reload();
}

/**
 * raw when it is a path on this site ("/library?x=1"), else null: never an
 * absolute URL, a protocol-relative "//host" or a "/\host" that browsers read
 * as one.
 */
export function safeRelativePath(raw: string | null | undefined): string | null {
  if (typeof raw !== "string" || raw === "") return null;
  if (!raw.startsWith("/")) return null;
  if (raw.startsWith("//") || raw.startsWith("/\\")) return null;
  // control characters (a newline or tab inside the path) are never a real return path
  for (let i = 0; i < raw.length; i++) {
    const c = raw.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return null;
  }
  return raw;
}

/** u when it is an http(s) address (a machine's reported web address), else null. */
export function safeHttpUrl(u: string | null | undefined): string | null {
  if (typeof u !== "string") return null;
  try {
    const url = new URL(u);
    return url.protocol === "http:" || url.protocol === "https:" ? u : null;
  } catch {
    return null;
  }
}
