// maktaba.tv/install/synology (Epic 59 story 59.09): a household's answers → the Container
// Manager project's YAML. Built in the page; nothing is sent anywhere.
//
// The file's body (from `services:` on) is the one deploy/nas/generate.py writes from the
// plain Docker compose (deploy/nas/synology/compose{,.j4125}.yaml); synology.test.ts checks
// that the defaults below give exactly those files. Change both together.

export const IMAGE = "ghcr.io/hamza-labs-core/maktaba-server";

export type Arch = "amd64" | "arm64";
export type Tier = "light" | "standard" | "strong";
/** What a NAS's memory means for Maktaba: its tier, or storage only (under the floor). */
export type Fit = Tier | "storage";

export interface Model {
  name: string;
  cpu: string;
  arch: Arch;
  /** Quick Sync through VA-API (the J4125 file); false: video in software */
  gpu: boolean;
  /** memory it ships with, and the most it takes (GB) */
  ram: number;
  maxRam: number;
}

// The guide's model table (story 59.14: data, checked once per release cycle). Maktaba never
// trusts it: the server detects the memory and the GPU it gets (59.04, 59.05).
export const MODELS: Model[] = [
  { name: "DS425+", cpu: "Intel Celeron J4125", arch: "amd64", gpu: true, ram: 2, maxRam: 6 },
  { name: "DS224+", cpu: "Intel Celeron J4125", arch: "amd64", gpu: true, ram: 2, maxRam: 6 },
  { name: "DS423+", cpu: "Intel Celeron J4125", arch: "amd64", gpu: true, ram: 2, maxRam: 6 },
  { name: "DS920+", cpu: "Intel Celeron J4125", arch: "amd64", gpu: true, ram: 4, maxRam: 8 },
  { name: "DS720+", cpu: "Intel Celeron J4125", arch: "amd64", gpu: true, ram: 2, maxRam: 6 },
  { name: "DS923+", cpu: "AMD Ryzen R1600", arch: "amd64", gpu: false, ram: 4, maxRam: 32 },
  { name: "DS723+", cpu: "AMD Ryzen R1600", arch: "amd64", gpu: false, ram: 2, maxRam: 32 },
  { name: "DS1522+", cpu: "AMD Ryzen R1600", arch: "amd64", gpu: false, ram: 8, maxRam: 32 },
  { name: "DS1621+", cpu: "AMD Ryzen V1500B", arch: "amd64", gpu: false, ram: 4, maxRam: 32 },
  { name: "DS223", cpu: "Realtek RTD1619B", arch: "arm64", gpu: false, ram: 2, maxRam: 2 },
  { name: "DS423", cpu: "Realtek RTD1619B", arch: "arm64", gpu: false, ram: 2, maxRam: 2 },
  { name: "DS223j", cpu: "Realtek RTD1619B", arch: "arm64", gpu: false, ram: 1, maxRam: 1 },
  { name: "DS124", cpu: "Realtek RTD1619B", arch: "arm64", gpu: false, ram: 1, maxRam: 1 },
];

/** The memory the kernel reports for installed GB (the GB → GiB step and the kernel's share):
 * a "4 GB" NAS reads about 3.6 GiB. */
export function reportedGiB(gb: number): number {
  return gb * 0.93 - 0.1;
}

/** The server's tier rule (cmd/maktaba-server/internal/hostinfo/tier.go) on the memory a NAS
 * with `gb` installed reports: storage under 3.5 GiB, Light under 6 GiB (8 on arm64),
 * Standard under 16 GiB, Strong from 16. */
export function fitFor(gb: number, arch: Arch): Fit {
  const g = reportedGiB(gb);
  if (g < 3.5) return "storage";
  if (g < 6 || (arch === "arm64" && g < 8)) return "light";
  if (g < 16) return "standard";
  return "strong";
}

/** Each tier's mem_limit: the container is held to it, so Maktaba plans for that memory and
 * DSM keeps the rest. Light's 4g is all of a 4 GB NAS; detection reads the smaller. */
export const MEM_LIMIT: Record<Tier, string> = { light: "4g", standard: "6g", strong: "16g" };

export interface Share {
  path: string;
  readOnly: boolean;
}

export interface Answers {
  /** the DSM user's uid (the group is always users, 100) */
  uid: string;
  gpu: boolean;
  /** the render device's group (stat -c %g /dev/dri/renderD128) */
  renderGid: string;
  tier: Tier;
  /** the volume holding docker/maktaba (an SSD volume when there is one) */
  volume: string;
  shares: Share[];
  /** optional: the NAS's LAN address and the time zone ("": left commented) */
  hostIp: string;
  tz: string;
  /** the image tag: latest, or a release's vX.Y.Z */
  tag: string;
}

export const DEFAULTS: Answers = {
  uid: "1026",
  gpu: false,
  renderGid: "937",
  tier: "standard",
  volume: "volume1",
  shares: [
    { path: "/volume1/Movies", readOnly: true },
    { path: "/volume1/Recordings", readOnly: false },
  ],
  hostIp: "",
  tz: "",
  tag: "latest",
};

export type Problem =
  | "uid"
  | "renderGid"
  | "volume"
  | "shares"
  | "sharePath"
  | "shareTwice"
  | "hostIp"
  | "tz"
  | "tag";

const VOLUME_RE = /^volume[1-9][0-9]?$/;
// /volumeN/<share>[/<folder>...]: no colon (the mount's separator), no quote, # or backslash
// (YAML), no space at either end of a part
const SHARE_RE = /^\/volume[1-9][0-9]?(\/[^/:#"'\\\n\s](?:[^/:#"'\\\n]*[^/:#"'\\\n\s])?)+$/u;
const IP_RE = /^((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;
const TZ_RE = /^[A-Za-z]+(\/[A-Za-z0-9_+-]+){0,2}$|^UTC$/;
const TAG_RE = /^(latest|v\d+\.\d+\.\d+)$/;

/** Every answer that cannot go in the file, with the share's index for share problems. */
export function problems(a: Answers): { problem: Problem; index?: number }[] {
  const out: { problem: Problem; index?: number }[] = [];
  const uid = Number(a.uid);
  if (!/^\d+$/.test(a.uid) || uid < 1 || uid > 2 ** 31) out.push({ problem: "uid" });
  if (a.gpu && !/^\d{1,10}$/.test(a.renderGid)) out.push({ problem: "renderGid" });
  if (!VOLUME_RE.test(a.volume)) out.push({ problem: "volume" });
  if (a.shares.length === 0) out.push({ problem: "shares" });
  const seen = new Set<string>();
  a.shares.forEach((s, index) => {
    const p = s.path.trim();
    if (!SHARE_RE.test(p)) out.push({ problem: "sharePath", index });
    else if (seen.has(p)) out.push({ problem: "shareTwice", index });
    seen.add(p);
  });
  if (a.hostIp && !IP_RE.test(a.hostIp)) out.push({ problem: "hostIp" });
  if (a.tz && !TZ_RE.test(a.tz)) out.push({ problem: "tz" });
  if (!TAG_RE.test(a.tag)) out.push({ problem: "tag" });
  return out;
}

/** The project's YAML for these answers (call only when problems() is empty). `about`
 * names what was answered, in the file's own comment (English: the file is read in
 * Container Manager's editor). */
export function renderCompose(a: Answers, about = ""): string {
  const head = [
    "# Maktaba on a Synology NAS: Container Manager → Project → Create, name maktaba, path",
    "# docker/maktaba, this file pasted as the project's YAML. Made on maktaba.tv/install/synology",
    "# from your answers" + (about ? `: ${about}` : "") + ".",
    "",
  ];
  const env = [
    "      MAKTABA_PLATFORM: dsm",
    a.hostIp
      ? `      MAKTABA_HOST_IP: ${a.hostIp}`
      : "      # MAKTABA_HOST_IP: 192.168.1.20   # this NAS's address on your network",
    a.tz ? `      TZ: ${a.tz}` : "      # TZ: Asia/Riyadh                 # your time zone",
  ];
  const body = [
    "services:",
    "  maktaba:",
    `    image: ${IMAGE}:${a.tag}`,
    "    container_name: maktaba",
    "    # the DSM user made for Maktaba (its uid, and the users group, 100)",
    `    user: "${a.uid}:100"`,
    "    network_mode: host",
    "    restart: unless-stopped",
    "    stop_grace_period: 90s",
    "    # the memory tier's limit: Light 4g, Standard 6g, Strong 16g",
    `    mem_limit: ${MEM_LIMIT[a.tier]}`,
    "    environment:",
    ...env,
    "    volumes:",
    "      # everything Maktaba keeps (an SSD volume's folder when there is one)",
    `      - /${a.volume}/docker/maktaba:/var/lib/maktaba`,
    "      # each share at its own path; :ro where Maktaba may only read",
    ...a.shares.map((s) => {
      const p = s.path.trim();
      return `      - ${p}:${p}${s.readOnly ? ":ro" : ""}`;
    }),
  ];
  const gpu = a.gpu
    ? [
        "    # J4125 (Quick Sync): the render device and its group, so the DSM user may open it.",
        "    # The group's id differs from NAS to NAS: over SSH, stat -c %g /dev/dri/renderD128",
        "    devices:",
        "      - /dev/dri:/dev/dri",
        "    group_add:",
        `      - "${a.renderGid}"`,
      ]
    : [];
  return [...head, ...body, ...gpu].join("\n") + "\n";
}

/** The file's body, from `services:` on (what the committed files and the page share). */
export function body(yaml: string): string {
  return yaml.slice(yaml.indexOf("services:"));
}
