// Epic 42.5: the SPA's edition, picked at build time by VITE_MAKTABA_EDITION.
//
//   full    the home's own web UI (the default, what the server ships): watching, and
//           the server pages in one "Manage" section for admins
//   player  watching only: no server route, nav item or link, and no server page code
//           in the bundle (scripts/check-player-bundle.mjs fails the build if there is)
//   remote  opens on the home's status; the server pages first, watching still there
//
// An edition only hides: the API refuses non-admins on every server route whatever
// the edition.
//
// The comparisons below read import.meta.env directly so Vite turns them into literals
// at build time and Rollup drops the server code from a player bundle; vite.config.ts
// refuses an unknown value, so a typo never ships as "full".
export type Edition = "full" | "player" | "remote";

export const EDITIONS: readonly Edition[] = ["full", "player", "remote"];

export const IS_PLAYER = import.meta.env.VITE_MAKTABA_EDITION === "player";
export const IS_REMOTE = import.meta.env.VITE_MAKTABA_EDITION === "remote";

export const EDITION: Edition = IS_PLAYER ? "player" : IS_REMOTE ? "remote" : "full";

/** The server pages (processing, transcription, settings, admin) are in this build. */
export const SERVER_PAGES = !IS_PLAYER;
