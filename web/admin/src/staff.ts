// The header every state-changing console request carries besides the browser's own Origin:
// the admin listener answers 403 bad_origin without both (story 50.32 §4). A cross-site form
// can't set a custom header, and a cross-site fetch that sets one needs a preflight, which the
// listener refuses.
export const STAFF_HEADERS: Record<string, string> = { "X-Maktaba-Staff": "1" };
