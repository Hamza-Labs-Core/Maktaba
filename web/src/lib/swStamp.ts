// The service worker's build stamp (vite.config.ts stamps dist/sw.js after every build).
// public/sw.js is byte-identical across releases otherwise, so a browser would never
// install the new worker (no reload toast) and the cached shell would keep pointing at the
// previous release's hashed assets. The stamp is the release and a hash of the built
// index.html, which names every hashed asset: any new build is a new worker.

/** `const BUILD = "…";` in public/sw.js. */
const BUILD_LINE = /const BUILD = "[^"]*";/;

/** sw.js with its BUILD set to `build`; throws when the line is missing (a build that
 * cannot stamp the worker fails rather than ship one that never updates). */
export function stampServiceWorker(src: string, build: string): string {
  if (!BUILD_LINE.test(src)) {
    throw new Error('sw.js has no `const BUILD = "…";` line to stamp');
  }
  return src.replace(BUILD_LINE, `const BUILD = ${JSON.stringify(build)};`);
}

/** The stamp: the release (or "dev") and the first 12 hex characters of the shell's hash. */
export function serviceWorkerBuild(version: string, shellHash: string): string {
  return `${version.replace(/[^0-9A-Za-z.+-]/g, "") || "dev"}-${shellHash.slice(0, 12)}`;
}
