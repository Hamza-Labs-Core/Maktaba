// The NAS model table of maktaba.tv's guides (Epic 59 story 59.14): nas-models.json is the data,
// checked once per release cycle; this file reads it and says, for each model, the tier the
// server will pick (cmd/maktaba-server/internal/hostinfo/tier.go) at the memory it ships with,
// and whether it fits, fits with more memory, or serves best as storage. Maktaba itself never
// trusts the table: the server detects the memory and the GPU it gets (59.04, 59.05).

import data from "./nas-models.json";

export type Arch = "amd64" | "arm64";
export type Vendor = "synology" | "qnap" | "ugreen" | "asustor" | "other";

export interface Model {
  vendor: Vendor;
  name: string;
  cpu: string;
  arch: Arch;
  /** video converted in hardware (Quick Sync or VA-API through /dev/dri) */
  gpu: boolean;
  /** the memory it ships with, and the most it takes (GB) */
  ram: number;
  maxRam: number;
}

export const MODELS: Model[] = data.models as Model[];
export const CHECKED: string = data.checked;

export const modelsOf = (vendor: Vendor): Model[] => MODELS.filter((m) => m.vendor === vendor);

/** The memory the kernel reports for installed GB (the GB → GiB step and the kernel's share):
 * a "4 GB" NAS reads about 3.6 GiB. */
export function reportedGiB(gb: number): number {
  return gb * 0.93 - 0.1;
}

export type DetectedTier = "light" | "standard" | "strong" | "storage";

/** The tier the server will pick on the memory it gets, the container's limit or the NAS's own,
 * whichever is smaller; "storage" under setup's 3.5 GiB floor. */
export function detectedTier(gb: number, arch: Arch, limitGiB = Infinity): DetectedTier {
  const g = Math.min(reportedGiB(gb), limitGiB);
  if (g < 3.5) return "storage";
  if (g < 6 || (arch === "arm64" && g < 8)) return "light";
  if (g < 16) return "standard";
  return "strong";
}

export type Fit =
  | { kind: "fits"; tier: Exclude<DetectedTier, "storage"> }
  | { kind: "more"; tier: Exclude<DetectedTier, "storage">; maxRam: number }
  | { kind: "storage" };

/** Fits (at the memory it ships with), fits with more memory (at the most it takes), or use it
 * as storage for a Maktaba home on another computer. */
export function fitOf(m: Model): Fit {
  const t = detectedTier(m.ram, m.arch);
  if (t !== "storage") return { kind: "fits", tier: t };
  const more = detectedTier(m.maxRam, m.arch);
  return more === "storage" ? { kind: "storage" } : { kind: "more", tier: more, maxRam: m.maxRam };
}
