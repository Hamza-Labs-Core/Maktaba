// Epic 58 story 58.01 — the launcher's copy of the compatibility decision.
//
// Before the launcher loads a home's /tv/, it reads api_level, api_level_oldest and
// client_floor from GET /api/discovery/home and decides, like every other app's copy (Go
// api/internal/compat, Kotlin, Swift): carry on, Update the home, or Update this app. All of
// them run the cases in shared/compat/vectors.json (compat.test.ts here) and must agree.

/** What the launcher does at a home. */
export type Verdict = "ok" | "update_home" | "update_app";

/** The oldest build of each app a home serves, keyed "<app>/<platform>". */
export type Floor = Record<string, string>;

/** What a home said; a home older than 58.01 says none of it (levels 0, no floor). */
export interface CompatHome {
  api_level?: number;
  api_level_oldest?: number;
  client_floor?: Floor | null;
}

/** What an app knows about itself. */
export interface CompatApp {
  name: string;
  platform: string;
  version: string;
  min_api_level: number;
  built_for_api_level: number;
}

/** The leading digits of each dot-separated part, up to the first part with none. */
function versionParts(v: string): number[] | null {
  let s = v.trim();
  if (s.charAt(0) === "v") s = s.slice(1);
  const out: number[] = [];
  for (const p of s.split(".")) {
    const m = /^\d+/.exec(p);
    if (!m) break;
    out.push(Number(m[0]));
    if (m[0].length < p.length) break;
  }
  return out.length > 0 ? out : null;
}

/** -1, 0 or 1; null when either has no number to read. A missing part is 0. */
export function compareVersions(a: string, b: string): number | null {
  const pa = versionParts(a);
  const pb = versionParts(b);
  if (!pa || !pb) return null;
  for (let i = 0; i < pa.length || i < pb.length; i++) {
    const x = pa[i] ?? 0;
    const y = pb[i] ?? 0;
    if (x < y) return -1;
    if (x > y) return 1;
  }
  return 0;
}

/** Whether version is below the floor for app/platform (never, when it cannot be read). */
export function belowFloor(floor: Floor | null | undefined, app: CompatApp): boolean {
  const min = floor ? floor[`${app.name}/${app.platform}`] : undefined;
  if (min === undefined) return false;
  const c = compareVersions(app.version, min);
  return c !== null && c < 0;
}

/** What app does at home (the order matters: Update the home comes first). */
export function decide(home: CompatHome, app: CompatApp): Verdict {
  const level = typeof home.api_level === "number" ? home.api_level : 0;
  const oldest = typeof home.api_level_oldest === "number" ? home.api_level_oldest : 0;
  if (level < app.min_api_level) return "update_home";
  if (app.built_for_api_level < oldest) return "update_app";
  if (belowFloor(home.client_floor, app)) return "update_app";
  return "ok";
}
