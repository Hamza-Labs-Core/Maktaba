// maktaba.tv/install/docker: the published Docker compose
// (cloud/installers/docker/docker-compose.yml) filled in from a household's answers, in the
// page, as Epic 59 story 59.11 describes. The file's optional settings are commented blocks
// headed `# [name] ...`; turning one on removes the "# " in front of its settings (the same
// rule as tests/nas/install_files.py, which CI runs on every variant with `docker compose
// config`). Nothing here sends anything anywhere.

export type Tier = "light" | "standard" | "strong" | "none";
export type Video = "none" | "intel-amd" | "nvidia";
export type Network = "host" | "bridge";

export interface MediaFolder {
  path: string;
  readOnly: boolean;
}

export interface Answers {
  /** uid:gid, never root */
  user: string;
  /** a host folder for Maktaba's data; "" keeps the named volume */
  data: string;
  media: MediaFolder[];
  tier: Tier;
  video: Video;
  /** the render device's group id (Intel or AMD video) */
  renderGroup: string;
  tz: string;
  network: Network;
  /** this machine's address (bridge network) */
  hostIp: string;
}

/** The memory limit each tier sets (story 59.04's tiers: Light under 6 GiB, Standard under 16). */
export const MEM_LIMIT: Record<Exclude<Tier, "none">, string> = {
  light: "4g",
  standard: "6g",
  strong: "16g",
};

/** The tier for a machine with this much RAM (GB, as sold): Maktaba leaves the rest to the NAS. */
export function tierFor(ramGB: number): Tier {
  if (!Number.isFinite(ramGB) || ramGB <= 0) return "none";
  if (ramGB < 8) return "light";
  if (ramGB < 24) return "standard";
  return "strong";
}

export type Problem =
  | "user"
  | "user-root"
  | "data"
  | "data-volume-user"
  | "media"
  | "media-none"
  | "render-group"
  | "tz"
  | "host-ip";

const HEADER = /^(\s*)# \[([a-z0-9-]+)\]/;
const SETTING = /^(\s*)# (\s*[A-Za-z_][A-Za-z0-9_.-]*:(?:\s|$)|\s*- )/;

/** Each block's commented setting lines (indexes into the file's lines). */
export function blocks(text: string): Map<string, number[]> {
  const out = new Map<string, number[]>();
  let current = "";
  text.split("\n").forEach((line, i) => {
    const h = HEADER.exec(line);
    if (h) {
      current = h[2];
      if (!out.has(current)) out.set(current, []);
      return;
    }
    if (!line.trimStart().startsWith("#")) {
      current = "";
      return;
    }
    if (current && SETTING.test(line)) out.get(current)!.push(i);
  });
  return out;
}

/** The text with these blocks turned on (bridge also drops `network_mode: host`). */
export function enable(text: string, names: string[]): string {
  const found = blocks(text);
  let lines = text.split("\n");
  for (const n of names) {
    const at = found.get(n);
    if (!at || at.length === 0) throw new Error(`no block [${n}] in the compose file`);
    for (const i of at) lines[i] = lines[i].replace(/^(\s*)# /, "$1");
  }
  if (names.includes("bridge")) lines = lines.filter((l) => l.trim() !== "network_mode: host");
  return lines.join("\n");
}

const PATH = /^\/[^:\n"'#]*[^:\n"'#/ ]$|^\/$/;

/** What is wrong with the answers (empty: the file can be made). */
export function problems(a: Answers): Problem[] {
  const p: Problem[] = [];
  const u = /^(\d+):(\d+)$/.exec(a.user.trim());
  if (!u) p.push("user");
  else if (u[1] === "0") p.push("user-root");
  if (a.data.trim() && !PATH.test(a.data.trim())) p.push("data");
  // A new Docker volume takes the image's folder, owned by uid 1000: any other user stops at
  // once, unable to write it (found by following this page). Another user names its folder.
  if (!a.data.trim() && u && u[1] !== "0" && u[1] !== "1000") p.push("data-volume-user");
  const media = a.media.filter((m) => m.path.trim());
  if (media.length === 0) p.push("media-none");
  if (media.some((m) => !PATH.test(m.path.trim()) || m.path.trim() === "/")) p.push("media");
  if (a.video === "intel-amd" && !/^\d+$/.test(a.renderGroup.trim())) p.push("render-group");
  if (!/^[A-Za-z0-9_+\-/]+$/.test(a.tz.trim())) p.push("tz");
  if (a.network === "bridge" && !/^\d{1,3}(\.\d{1,3}){3}$/.test(a.hostIp.trim())) p.push("host-ip");
  return p;
}

function replaceOnce(text: string, from: string, to: string): string {
  const at = text.indexOf(from);
  if (at < 0 || text.indexOf(from, at + 1) >= 0) {
    throw new Error(`the compose file no longer has ${JSON.stringify(from.trim())} once`);
  }
  return text.slice(0, at) + to + text.slice(at + from.length);
}

/** The published compose file filled in from the answers. Throws on answers with problems. */
export function fillCompose(base: string, a: Answers): string {
  const bad = problems(a);
  if (bad.length) throw new Error(`answers with problems: ${bad.join(", ")}`);
  const on: string[] = [];
  if (a.tier !== "none") on.push("memory");
  if (a.network === "bridge") on.push("bridge");
  if (a.video === "intel-amd") on.push("dri");
  if (a.video === "nvidia") on.push("nvidia");
  let t = enable(base, on);
  t = replaceOnce(t, 'user: "1000:1000"', `user: "${a.user.trim()}"`);
  t = replaceOnce(t, "TZ: Etc/UTC", `TZ: ${a.tz.trim()}`);
  if (a.tier !== "none") t = replaceOnce(t, "mem_limit: 6g", `mem_limit: ${MEM_LIMIT[a.tier]}`);
  if (a.network === "bridge") {
    t = replaceOnce(t, "MAKTABA_HOST_IP: 192.168.1.20", `MAKTABA_HOST_IP: ${a.hostIp.trim()}`);
  }
  if (a.video === "intel-amd") t = replaceOnce(t, '- "937"', `- "${a.renderGroup.trim()}"`);
  if (a.data.trim()) {
    t = replaceOnce(t, "- maktaba:/var/lib/maktaba", `- ${a.data.trim()}:/var/lib/maktaba`);
    t = replaceOnce(t, "\nvolumes:\n  maktaba:\n", "\n");
  }
  const indent = /^(\s*)- \/srv\/media:\/srv\/media$/m.exec(t)?.[1] ?? "      ";
  const lines = a.media
    .filter((m) => m.path.trim())
    .map((m) => {
      const p = m.path.trim();
      return `${indent}- ${p}:${p}${m.readOnly ? ":ro" : ""}`;
    });
  t = replaceOnce(t, `${indent}- /srv/media:/srv/media`, lines.join("\n"));
  return t;
}
