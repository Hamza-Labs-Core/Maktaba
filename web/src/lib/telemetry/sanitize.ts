// Story 16.5: what leaves the app in an error report. The home strips the
// libraries' roots, paths and addresses again (story 16.7); this removes what
// only the client sees:
// home folders, the server's address, query strings (a search query lives in
// one), and every frame after the fifth.

const HOME_UNIX = /\/(?:Users|home)\/[^/\s]+/g;
const HOME_WINDOWS = /[A-Za-z]:\\Users\\[^\\\s]+/g;
// an origin (http://192.168.1.20:8088): the server's address names the home
const ORIGIN = /\b(?:https?|wss?|file|capacitor|tauri):\/\/[^/\s)]*/gi;
// a query string or fragment after a path
const QUERY = /[?#][^\s)]*/g;

/** sanitizeMessage removes home folders, origins and query strings from s. */
export function sanitizeMessage(s: string, max = 1000): string {
  const out = s
    .replace(ORIGIN, "<origin>")
    .replace(QUERY, "")
    .replace(HOME_WINDOWS, "<home>")
    .replace(HOME_UNIX, "<home>");
  return out.length > max ? out.slice(0, max) : out;
}

/** firstFrames is the stack's first n frames (the message line left out), sanitized. */
export function firstFrames(stack: string | undefined, n = 5): string {
  if (!stack) return "";
  const frames = stack
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.startsWith("at ") || l.includes("@"));
  return frames
    .slice(0, n)
    .map((l) => sanitizeMessage(l, 300))
    .join("\n");
}

/** routeFeature is the feature a route belongs to: its first segment when it is a known one. */
const FEATURES = new Set([
  "library",
  "series",
  "videos",
  "search",
  "guide",
  "live",
  "queue",
  "transcription",
  "settings",
  "account",
  "help",
  "admin",
  "pair",
]);

export function routeFeature(pathname: string): string | null {
  const parts = pathname.split("/").filter(Boolean);
  const first = parts[0] ?? "";
  if (first === "videos" && parts[2] === "watch") return "player";
  return FEATURES.has(first) ? first : null;
}
