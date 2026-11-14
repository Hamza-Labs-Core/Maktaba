// Epic 58 — the launcher's own WebVTT reader, for the shared subtitle layer (stories 58.10
// and 58.11). The TV's player is never asked to draw subtitles: Samsung's AVPlay hands its
// text to the app and LG's <video> draws Arabic poorly, so the launcher reads the cues and
// draws them itself (layer.ts).
//
// It reads what the home writes (story 48.4's WebVTT renditions and sidecar files): the
// header, NOTE, STYLE and REGION blocks skipped, cue ids, hh:mm:ss.ttt or mm:ss.ttt times,
// cue settings ignored, and the cue text with its tags removed (<i>, <b>, <c.x>, <v Name>,
// <ruby>, inline timestamps): the layer shows plain text, set with textContent, so no
// markup from a file is ever run.

export interface Cue {
  /** Seconds. */
  start: number;
  end: number;
  /** Plain text; lines separated by "\n". */
  text: string;
}

const TIME = /^(?:(\d+):)?([0-5]?\d):([0-5]\d)[.,](\d{1,3})$/;

/** A WebVTT timestamp in seconds (NaN when it is not one). */
export function parseTime(s: string): number {
  const m = TIME.exec(s.trim());
  if (!m) return NaN;
  const h = m[1] ? Number(m[1]) : 0;
  const ms = Number(m[4]!.padEnd(3, "0"));
  return h * 3600 + Number(m[2]) * 60 + Number(m[3]) + ms / 1000;
}

const ENTITIES: Record<string, string> = {
  "&amp;": "&",
  "&lt;": "<",
  "&gt;": ">",
  "&quot;": '"',
  "&apos;": "'",
  "&nbsp;": " ",
  "&lrm;": "‎",
  "&rlm;": "‏",
};

/** A cue's text without its tags, entities decoded. */
export function cueText(raw: string): string {
  return raw
    .replace(/<[^>]*>/g, "")
    .replace(/&(?:amp|lt|gt|quot|apos|nbsp|lrm|rlm);/g, (e) => ENTITIES[e] ?? e)
    .trim();
}

/** The cues of a WebVTT file, in file order ([] when it is not WebVTT). */
export function parseVtt(text: string): Cue[] {
  const src = text.replace(/^\uFEFF/, "").replace(/\r\n?/g, "\n");
  if (!/^WEBVTT(?:[ \t\n]|$)/.test(src)) return [];
  const blocks = src.split(/\n{2,}/);
  const cues: Cue[] = [];
  for (let b = 1; b < blocks.length; b++) {
    const lines = blocks[b]!.split("\n").filter((l, i) => i > 0 || l.trim() !== "");
    if (lines.length === 0) continue;
    const head = lines[0]!;
    if (/^(NOTE|STYLE|REGION)(?:[ \t]|$)/.test(head)) continue;
    // an optional cue id, then the timing line
    const at = head.includes("-->") ? 0 : lines[1]?.includes("-->") ? 1 : -1;
    if (at < 0) continue;
    const timing = lines[at]!.split("-->");
    const start = parseTime(timing[0]!);
    const end = parseTime((timing[1] ?? "").trim().split(/[ \t]+/)[0] ?? "");
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) continue;
    const body = cueText(lines.slice(at + 1).join("\n"));
    if (body) cues.push({ start, end, text: body });
  }
  return cues;
}

/** The cues showing at time t (start ≤ t < end), in file order. */
export function cuesAt(cues: readonly Cue[], t: number): Cue[] {
  return cues.filter((c) => c.start <= t && t < c.end);
}
