// The last answer each page loaded (Home, Libraries, a library's grid, a
// video's page, ...), so going back to a page shows it at once while it reads
// again quietly (lib/usePageData). In memory only, never in storage, and only
// for the user who loaded it: an answer asked for by another user (or by
// nobody) is not there, and the whole cache is dropped when the user changes
// or signs out (lib/auth). It holds the MAX_ANSWERS most recently used
// answers; a change a page makes drops the answers it makes stale
// (forgetAnswers with the keys' prefixes).

// Keys start with what the page shows, so a change can drop a family of them:
//   home, libraries, hub:<library>, library-collections:<library>:<kind>,
//   library-series:<library>:<sort>, collection:<kind>:<library>:<id>,
//   collections:<library>, collection:<id>, playlists, playlist:<id>,
//   videos:<query> (a library's grid), video:<id>, series-list:<query>,
//   series:<id>, history:<kind>, search:<query>, guide:<day>, account:<page>,
//   admin:<page>.

/** The answers that show videos and their state (watched, favorite, in a list, a title). */
export const VIDEO_ANSWERS = [
  "home",
  "hub:",
  "videos:",
  "video:",
  "series",
  "history:",
  "search:",
  "playlist",
  "collection",
  "library-collections:",
  "library-series:",
];

/** How many answers are kept: the least recently used one goes first. */
export const MAX_ANSWERS = 50;

// a Map keeps insertion order: a read or write moves the key to the end
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
  if (!answers.has(key)) return undefined;
  const value = answers.get(key);
  answers.delete(key);
  answers.set(key, value);
  return value as T;
}

/** Keep the page's answer for this user (signed out: nothing is kept). */
export function keepAnswer(user: string | null | undefined, key: string, value: unknown): void {
  if (!user) return;
  ownedBy(user);
  answers.delete(key);
  answers.set(key, value);
  while (answers.size > MAX_ANSWERS) {
    answers.delete(answers.keys().next().value!);
  }
}

/**
 * Drop kept answers: every one (sign-out, another user), or those whose key
 * starts with one of `prefixes` (a change made them stale: a renamed
 * playlist drops "playlist:<id>" and "playlists").
 */
export function forgetAnswers(...prefixes: string[]): void {
  if (prefixes.length === 0) {
    answers.clear();
    owner = null;
    return;
  }
  for (const key of [...answers.keys()]) {
    if (prefixes.some((p) => key.startsWith(p))) answers.delete(key);
  }
}
