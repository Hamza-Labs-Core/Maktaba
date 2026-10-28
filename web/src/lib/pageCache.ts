// The last answer each page loaded (Home, Libraries, a library's page, ...),
// so going back to a page shows it at once while it reads again quietly
// (lib/usePageData). In memory only, never in storage, and only for the user
// who loaded it: an answer asked for by another user (or by nobody) is not
// there, and the whole cache is dropped when the user changes or signs out
// (lib/auth).

const answers = new Map<string, unknown>();
let owner: string | null = null;

// the cache belongs to one user at a time: another user starts it empty
function ownedBy(user: string): void {
  if (owner === user) return;
  answers.clear();
  owner = user;
}

/** The page's last answer for this user, if it has one. */
export function cachedAnswer<T>(user: string | null | undefined, key: string): T | undefined {
  if (!user) return undefined;
  ownedBy(user);
  return answers.get(key) as T | undefined;
}

/** Keep the page's answer for this user (signed out: nothing is kept). */
export function keepAnswer(user: string | null | undefined, key: string, value: unknown): void {
  if (!user) return;
  ownedBy(user);
  answers.set(key, value);
}

/** Drop every kept answer (sign-out, another user). */
export function forgetAnswers(): void {
  answers.clear();
  owner = null;
}
