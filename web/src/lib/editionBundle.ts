// Epic 42.5: which built modules are server page code, for the player build's check
// (vite.config.ts playerBundleCheck). Kept here, beside lib/edition.ts, so a test
// covers the pattern.

// The server pages: processing (pages/queue), transcription, settings, the admin and
// cloud pages, the routes module that loads them and the remote's status page.
const SERVER_PAGE =
  /\/src\/(serverRoutes\.tsx|pages\/(queue|transcription|Admin|Settings|Cloud)\/|pages\/(Settings|RemoteHome)\.tsx)/;

/** The ids of server page modules that put code in a bundle, normalised to "/". */
export function serverPageModules(modules: ReadonlyArray<{ id: string; size: number }>): string[] {
  return modules
    .map((m) => ({ ...m, id: m.id.replace(/\\/g, "/") }))
    .filter((m) => m.size > 0 && SERVER_PAGE.test(m.id))
    .map((m) => m.id);
}
