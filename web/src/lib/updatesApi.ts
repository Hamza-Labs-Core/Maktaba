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

/** Numeric comparison of the version cores (a prerelease ranks below its release). */
export function compareVersions(a: string, b: string): number {
  const pa = VERSION_RE.exec(a.trim());
  const pb = VERSION_RE.exec(b.trim());
  if (!pa || !pb) return 0;
  for (let i = 1; i <= 3; i++) {
    const d = Number(pa[i]) - Number(pb[i]);
    if (d !== 0) return d > 0 ? 1 : -1;
  }
  const preA = a.includes("-");
  const preB = b.includes("-");
  if (preA === preB) return 0;
  return preA ? -1 : 1;
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
