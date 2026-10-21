// The home's automatic updates (Epic 35, story 35.3), admin only:
//
//   GET  /api/cluster/updates            policy, the next planned update, the catalog
//   PUT  /api/cluster/updates/settings   rules.updates keys (400 pin_below_installed, bad_window, …)
//   POST /api/cluster/updates/run        {version?, confirm:true}  Update now -> 202 {run_id}
//   POST /api/cluster/updates/skip       {version}
//   POST /api/cluster/updates/defer      {until}
//   POST /api/cluster/updates/check      check GitHub now
import { api } from "./api";

export interface UpdatesPolicy {
  auto_install: boolean;
  channel: "stable" | "beta";
  window: string;
  window_overrun_minutes: number;
  min_release_age_hours: number;
  pinned_version: string;
  deferred_until: string | null;
  skipped_versions: string[];
  node_failure_policy: string;
  keep_versions: number;
}

export interface CatalogRelease {
  version: string;
  tag: string;
  channel: string;
  published_at?: string;
  release_url?: string;
  notes?: string;
  verify_state: "unverified" | "verified" | "failed";
  verify_error?: { kind: string; file?: string; detail?: string };
  withdrawn_at?: string;
  held_at?: string;
  approved_at?: string;
  manifest?: { breaks_previous_home?: boolean; min_upgrade_from?: string };
}

export interface UpdatesPlan {
  target?: CatalogRelease;
  newest?: CatalogRelease;
  held: boolean;
  stepping_stone: boolean;
  reason?: string;
  auto_install: boolean;
  due: boolean;
  next_run_at?: string;
  eligible_at?: string;
  waiting?: string;
}

export interface UpdatesOverview {
  current_version: string;
  watermark: string;
  policy: UpdatesPolicy;
  auto_install_available: boolean;
  plan: UpdatesPlan;
  run?: { id: string; version: string; state: string; step: string };
  catalog: CatalogRelease[];
  last_check_ok?: string;
  last_check_error?: string;
  check_stale: boolean;
  time_zone: string;
}

export const updatesApi = {
  overview: () => api.get<UpdatesOverview>("/api/cluster/updates"),
  saveSettings: (set: Partial<Record<keyof UpdatesPolicy, unknown>>) =>
    api.put<{ section: string; changed: string[] }>("/api/cluster/updates/settings", set),
  runNow: (version?: string) =>
    api.post<{ run_id: string; version: string }>("/api/cluster/updates/run", {
      ...(version ? { version } : {}),
      confirm: true,
    }),
  skip: (version: string) => api.post("/api/cluster/updates/skip", { version }),
  defer: (until: string) => api.post("/api/cluster/updates/defer", { until }),
  check: () => api.post("/api/cluster/updates/check", {}),
};

/** A quiet window "HH:MM-HH:MM" (may cross midnight), longer than 30 minutes. */
export function validWindow(s: string): boolean {
  const m = /^\s*(\d{1,2}):(\d{2})\s*-\s*(\d{1,2}):(\d{2})\s*$/.exec(s);
  if (!m) return false;
  const [h1, m1, h2, m2] = m.slice(1).map(Number) as [number, number, number, number];
  if (h1 > 23 || h2 > 23 || m1 > 59 || m2 > 59) return false;
  const len = (h2 * 60 + m2 - (h1 * 60 + m1) + 1440) % 1440;
  return len > 30;
}

const VERSION_RE = /^v?(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?$/;

/** A release version ("1.5.0", "v1.5.0-rc.1"). */
export function validVersion(s: string): boolean {
  return VERSION_RE.test(s.trim());
}

/** A `git describe` build once normalized: a tag, the commits after it, the commit. */
const DESCRIBE_RE = /^(\d+\.\d+\.\d+(?:-[0-9a-z.]+)?)-(\d+)-g[0-9a-f]{4,40}$/;

/** A version's release tag (no "v", no build metadata), its commits after the tag and
 * whether its tree was dirty: 0.2.0-12-gabc1234 is 12 commits after 0.2.0 (the server's
 * releaseverify.splitDescribe). */
function splitDescribe(v: string): { tag: string; commits: number; dirty: boolean } {
  let s = v.trim().toLowerCase().replace(/^v/, "");
  const plus = s.indexOf("+");
  if (plus >= 0) s = s.slice(0, plus);
  let dirty = false;
  if (s.endsWith("-dirty")) {
    dirty = true;
    s = s.slice(0, -"-dirty".length);
  }
  const m = DESCRIBE_RE.exec(s);
  return m ? { tag: m[1] ?? s, commits: Number(m[2]), dirty } : { tag: s, commits: 0, dirty };
}

const sign = (n: number): number => (n > 0 ? 1 : n < 0 ? -1 : 0);

/** Two release tags: the numeric core, then a prerelease below its release, prereleases by
 * their dot-separated identifiers (numeric ones numerically, below words). */
function compareReleases(a: string, b: string): number {
  const [ac = "", ...ap] = a.split("-");
  const [bc = "", ...bp] = b.split("-");
  const an = ac.split(".").map(Number);
  const bn = bc.split(".").map(Number);
  for (let i = 0; i < Math.max(an.length, bn.length); i++) {
    const d = (an[i] ?? 0) - (bn[i] ?? 0);
    if (d !== 0) return sign(d);
  }
  const apre = ap.join("-");
  const bpre = bp.join("-");
  if (apre === "" || bpre === "") return apre === bpre ? 0 : apre === "" ? 1 : -1;
  const as = apre.split(".");
  const bs = bpre.split(".");
  for (let i = 0; i < Math.min(as.length, bs.length); i++) {
    const x = as[i] ?? "";
    const y = bs[i] ?? "";
    const xn = /^\d+$/.test(x);
    const yn = /^\d+$/.test(y);
    if (xn && yn && Number(x) !== Number(y)) return sign(Number(x) - Number(y));
    if (xn !== yn) return xn ? -1 : 1;
    if (!xn && x !== y) return x < y ? -1 : 1;
  }
  return sign(as.length - bs.length);
}

/** The server's order of versions (releaseverify.CompareVersions): a prerelease ranks below
 * its release, and a `git describe` build after the tag it was built from (by its commits,
 * then dirty after clean). */
export function compareVersions(a: string, b: string): number {
  if (!VERSION_RE.test(a.trim()) || !VERSION_RE.test(b.trim())) return 0;
  const da = splitDescribe(a);
  const db = splitDescribe(b);
  const c = compareReleases(da.tag, db.tag);
  if (c !== 0) return c;
  if (da.commits !== db.commits) return sign(da.commits - db.commits);
  return da.dirty === db.dirty ? 0 : da.dirty ? 1 : -1;
}

/** Why a pin is refused ("" = fine): not a version, or below what is installed. */
export function pinProblem(pin: string, installed: string): "" | "bad_pin" | "pin_below_installed" {
  const p = pin.trim();
  if (p === "") return "";
  if (!validVersion(p)) return "bad_pin";
  if (installed && validVersion(installed) && compareVersions(p, installed) < 0) {
    return "pin_below_installed";
  }
  return "";
}
