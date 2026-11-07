// Where sign-in sends the buyer back to (`/signin?next=`): a page of this store only, never
// anywhere else. The value is resolved against the store's origin and kept only when it stays
// on that origin and its path is one of the pages that send people here: a buy page
// (/buy/<plugin>, story 54.19 §5) or the admin pages (/admin). Anything else, including a
// backslash or control character, an absolute or protocol-relative URL, a script URL, an
// empty or very long value, is null: the caller goes to its safe default.

/** The longest next accepted. */
export const NEXT_MAX = 512;

const ALLOWED = [/^\/buy\/[a-z0-9][a-z0-9.-]{0,127}\/?$/, /^\/admin(\/[A-Za-z0-9/_-]*)?$/];

export function safeNext(raw: string | null | undefined, storeOrigin: string): string | null {
  if (typeof raw !== "string" || raw.length === 0 || raw.length > NEXT_MAX) return null;
  // a backslash is a slash to browsers; control characters and spaces are stripped by them
  if (/[\\\u0000- \u007f]/.test(raw)) return null;
  if (!raw.startsWith("/") || raw.startsWith("//")) return null;
  let url: URL;
  try {
    url = new URL(raw, storeOrigin);
  } catch {
    return null;
  }
  if (url.origin !== storeOrigin) return null;
  if (!ALLOWED.some((re) => re.test(url.pathname))) return null;
  return url.pathname + url.search;
}
