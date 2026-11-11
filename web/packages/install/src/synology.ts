// maktaba.tv/install/synology (Epic 59 story 59.09): the Container Manager project's YAML,
// made from the published Docker compose (cloud/installers/docker/docker-compose.yml) with the
// same fill-in as /install/docker (compose.ts), then made Synology's: MAKTABA_PLATFORM=dsm and
// the Synology header. deploy/nas/generate.py writes deploy/nas/synology/compose{,.j4125}.yaml
// by the same steps; synology.test.ts checks that DEFAULTS give exactly those files.
// Nothing here sends anything anywhere.

import {
  fillCompose,
  problems as baseProblems,
  tierFor,
  type Problem as BaseProblem,
} from "./compose";
import { detectedTier, modelsOf } from "./nasModels";
export { detectedTier, reportedGiB } from "./nasModels";

export type { Arch } from "./nasModels";
import type { Arch } from "./nasModels";
export type Tier = "light" | "standard" | "strong";

export type { Model } from "./nasModels";

// The guide's model table: the Synology rows of nas-models.json (story 59.14: data, checked once
// per release cycle). Maktaba never trusts it: the server detects the memory and the GPU it gets
// (59.04, 59.05).
export const MODELS = modelsOf("synology");

/** The memory limit the page sets for a NAS with this much (compose.ts's rule: the rest is
 * DSM's), or "storage" when setup will refuse. */
export function limitTier(gb: number, arch: Arch): Tier | "storage" {
  if (detectedTier(gb, arch) === "storage") return "storage";
  const t = tierFor(gb);
  return t === "none" ? "storage" : t;
}

export interface Share {
  path: string;
  readOnly: boolean;
}

export interface SynAnswers {
  /** the DSM user's uid; the group is always users (100) */
  uid: string;
  gpu: boolean;
  renderGroup: string;
  tier: Tier;
  /** the volume holding docker/maktaba (an SSD volume when there is one) */
  volume: string;
  shares: Share[];
  tz: string;
}

export const DEFAULTS: SynAnswers = {
  uid: "1026",
  gpu: false,
  renderGroup: "937",
  tier: "standard",
  volume: "volume1",
  shares: [
    { path: "/volume1/Movies", readOnly: true },
    { path: "/volume1/Recordings", readOnly: false },
  ],
  tz: "Etc/UTC",
};

export type Problem = "uid" | "volume" | "share-volume" | BaseProblem;

const SHARE = /^\/volume[1-9][0-9]?\/[^/]/;

/** What is wrong with the answers (empty: the file can be made). */
export function problems(a: SynAnswers): Problem[] {
  const out: Problem[] = [];
  if (!/^[1-9]\d{0,9}$/.test(a.uid.trim())) out.push("uid");
  if (!/^volume[1-9][0-9]?$/.test(a.volume.trim())) out.push("volume");
  const shares = a.shares.filter((s) => s.path.trim());
  if (shares.some((s) => !SHARE.test(s.path.trim()))) out.push("share-volume");
  for (const p of baseProblems(base(a))) if (p !== "user") out.push(p);
  return out;
}

function base(a: SynAnswers) {
  return {
    user: `${a.uid.trim()}:100`,
    data: `/${a.volume.trim()}/docker/maktaba`,
    media: a.shares,
    tier: a.tier,
    video: a.gpu ? ("intel-amd" as const) : ("none" as const),
    renderGroup: a.renderGroup,
    tz: a.tz,
    network: "host" as const,
    hostIp: "",
  };
}

export const HEADER = `# Maktaba on a Synology NAS (DSM 7.2 or newer): Container Manager → Project → Create, name
# maktaba, path docker/maktaba, this file pasted as the project's YAML. maktaba.tv/install/synology
# fills in the DSM user, the shares, the memory and the time zone from your answers, and says how
# to make the user and its folders. Made from the published Docker compose
# (cloud/installers/docker/docker-compose.yml), as deploy/nas/generate.py makes it.
`;

/** The project's YAML (throws on answers with problems). */
export function synologyCompose(baseText: string, a: SynAnswers, version: string): string {
  const bad = problems(a);
  if (bad.length) throw new Error(`answers with problems: ${bad.join(", ")}`);
  let t = fillCompose(baseText, base(a));
  const from = "MAKTABA_PLATFORM: docker";
  if (t.split(from).length !== 2) throw new Error(`the compose file no longer has ${from} once`);
  t = t.replace(from, "MAKTABA_PLATFORM: dsm");
  return `${HEADER}# This version: ${version}.\n\n${t.slice(t.indexOf("services:"))}`;
}

/** The version line of each file. */
export const VERSION = {
  j4125: "J4125 models (Quick Sync through /dev/dri)",
  other: "models without Quick Sync (Ryzen, ARM, other Intel)",
};
