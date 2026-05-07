// Translate repo-rooted file paths from the wiki DB into URLs the wiki-app
// can fetch. The dev server and `vite preview` mount mockups at /mockups
// and diagrams at /diagrams; the build step copies them into dist/ as well.

const BASE = (import.meta.env.BASE_URL || './').replace(/\/$/, '');

function joinBase(suffix: string): string {
  if (!suffix.startsWith('/')) suffix = '/' + suffix;
  return `${BASE}${suffix}`;
}

/**
 * Convert a repo-rooted mockup path (e.g. `web/mockups/admin/login.html`)
 * into a URL the wiki-app can load. Returns null if the path doesn't look
 * like a mockup file.
 */
export function mockupUrl(repoPath: string): string | null {
  if (!repoPath) return null;
  const m = repoPath.match(/(?:^|\/)web\/mockups\/(.+)$/);
  if (!m) {
    // Fallback: maybe already relative to mockups root.
    if (/\.html?$/i.test(repoPath)) return joinBase(`/mockups/${repoPath.replace(/^\/+/, '')}`);
    return null;
  }
  return joinBase(`/mockups/${m[1]}`);
}

/**
 * Convert a repo-rooted diagram path (e.g. `specs/diagrams/auth-flow.drawio`)
 * into a URL the wiki-app can load. Returns null if the path doesn't look
 * like a diagram file.
 */
export function diagramUrl(repoPath: string): string | null {
  if (!repoPath) return null;
  const m = repoPath.match(/(?:^|\/)specs\/diagrams\/(.+)$/);
  if (!m) {
    if (/\.drawio$/i.test(repoPath)) return joinBase(`/diagrams/${repoPath.replace(/^\/+/, '')}`);
    return null;
  }
  return joinBase(`/diagrams/${m[1]}`);
}

/**
 * Pull all mockup-shaped paths out of an entry's `files` map.
 * Files entries can be string | string[] | null.
 */
export function collectMockupPaths(
  files: Record<string, string | string[] | null> | undefined,
): string[] {
  if (!files) return [];
  const out: string[] = [];
  for (const [, val] of Object.entries(files)) {
    const arr = Array.isArray(val) ? val : val ? [val] : [];
    for (const p of arr) {
      if (p && /\.html?$/i.test(p) && /web\/mockups\//.test(p)) out.push(p);
    }
  }
  return out;
}

export function collectDiagramPaths(
  files: Record<string, string | string[] | null> | undefined,
): string[] {
  if (!files) return [];
  const out: string[] = [];
  for (const [, val] of Object.entries(files)) {
    const arr = Array.isArray(val) ? val : val ? [val] : [];
    for (const p of arr) {
      if (p && /\.drawio$/i.test(p)) out.push(p);
    }
  }
  return out;
}
