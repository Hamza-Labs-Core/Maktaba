// Story 58.09 — what the launcher is, for the compatibility check (58.01) and the client
// header the home logs: `tv-web/<version> (tizen|webos; tv)`.

declare const __LAUNCHER_VERSION__: string | undefined;
declare const __BUNDLE_UI__: boolean | undefined;

/** The launcher's version (the release's, stamped at build; "dev" otherwise). */
export const LAUNCHER_VERSION: string =
  typeof __LAUNCHER_VERSION__ === "string" ? __LAUNCHER_VERSION__ : "dev";

/**
 * Built with --bundle-ui (README §11 decision 3, owner question Q3): a copy of /tv/ is in
 * the package (ui/index.html) and reaches the home's API with bearer tokens. The default is
 * the home-served UI, loaded from the home in the iframe.
 */
export const BUNDLE_UI: boolean = typeof __BUNDLE_UI__ === "boolean" ? __BUNDLE_UI__ : false;

/** The app name the home's floor and logs know the launcher by. */
export const APP_NAME = "tv-web";

/** The oldest home API level the launcher can use: the level that has /tv/'s contract. */
export const MIN_API_LEVEL = 1;

/** The level the launcher was built for. */
export const BUILT_FOR_API_LEVEL = 1;
