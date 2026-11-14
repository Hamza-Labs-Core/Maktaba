// The receiver builds on cast.maktaba.tv (/r/builds.json) and which one a sender gets (story
// 58.07, T04). A build at level N plays for a home at level N or newer: the loader picks the
// newest build whose level is at most the sender's `api_level`; a home older than every build
// gets the "Update the home" screen. A build is kept while its level is in the 12-month window
// the home keeps (58.01): until 12 months after a newer build replaced it.

export interface Build {
  /** The API level the build was made for. */
  level: number;
  /** Its path, "/r/<level>/". */
  path: string;
  /** When it was built (ISO 8601). */
  built: string;
  /** A hash of its files: an unchanged receiver is not deployed again. */
  hash?: string;
  /** Its files, relative to its path: the release copies a kept build from the live site. */
  files?: string[];
}

export interface Manifest {
  builds: Build[];
}

/** The months a replaced build is kept. */
export const KEEP_MONTHS = 12;

function valid(b: unknown): b is Build {
  if (!b || typeof b !== "object") return false;
  const x = b as Record<string, unknown>;
  return (
    Number.isInteger(x.level) &&
    (x.level as number) >= 1 &&
    typeof x.path === "string" &&
    typeof x.built === "string"
  );
}

/** The builds of a manifest, newest level first, the bad entries dropped. */
export function buildsOf(manifest: unknown): Build[] {
  const list = (manifest as Manifest | null)?.builds;
  return Array.isArray(list) ? list.filter(valid).sort((a, b) => b.level - a.level) : [];
}

/** The level a sender named, or 1 for a sender that named none (the first level). */
export function senderLevel(value: unknown): number {
  return typeof value === "number" && Number.isInteger(value) && value >= 1 ? value : 1;
}

/** The build for a sender's level: the newest at or below it, or null ("Update the home"). */
export function pickBuild(builds: readonly Build[], level: number): Build | null {
  let best: Build | null = null;
  for (const b of builds) {
    if (b.level <= level && (!best || b.level > best.level)) best = b;
  }
  return best;
}

function addMonths(d: Date, months: number): Date {
  const out = new Date(d.getTime());
  out.setUTCMonth(out.getUTCMonth() + months);
  return out;
}

/** The builds still kept at `now`: one per level (the newest built), and a level only until
 * KEEP_MONTHS after the next level's build replaced it. */
export function keptBuilds(builds: readonly Build[], now: Date): Build[] {
  const byLevel = new Map<number, Build>();
  for (const b of builds) {
    const had = byLevel.get(b.level);
    if (!had || Date.parse(b.built) > Date.parse(had.built)) byLevel.set(b.level, b);
  }
  const sorted = [...byLevel.values()].sort((a, b) => a.level - b.level);
  return sorted
    .filter((b, i) => {
      const next = sorted[i + 1];
      if (!next) return true;
      const replaced = new Date(next.built);
      return Number.isNaN(replaced.getTime()) || addMonths(replaced, KEEP_MONTHS) > now;
    })
    .sort((a, b) => b.level - a.level);
}

/** The manifest after deploying `fresh`: it replaces any build of its level, then the window
 * applies. */
export function withBuild(live: readonly Build[], fresh: Build, now: Date): Build[] {
  return keptBuilds([...live.filter((b) => b.level !== fresh.level), fresh], now);
}
