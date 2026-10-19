// Whole-page navigation (Epic 35): the cluster sign-in goes through the API
// (node -> home -> node), which the router cannot do. Kept in one module so
// tests can mock it (jsdom's window.location cannot be spied on).

/** Leaves the app for url (a full page load). */
export function assignLocation(url: string): void {
  window.location.assign(url);
}

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

/** The home's cluster sign-in step a node sent the user to (design §10.5). */
export const SSO_AUTHORIZE_PATH = "/api/cluster/sso/authorize";

/** Where a node starts the cluster sign-in, coming back to returnTo. */
export function ssoStartUrl(returnTo: string): string {
  return `/api/cluster/sso/start?return=${encodeURIComponent(returnTo)}`;
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
