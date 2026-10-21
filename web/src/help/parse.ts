// Reading a help topic's Markdown (Epic 41): its title, its anchored
// headings (`## What it is {#what-it-is}`), its links, and the sections
// search walks. Anchors are written in the source, never derived from the
// heading text, so English and Arabic share one URL.

const HEADING = /^\s{0,3}(#{1,6})\s+(.*?)\s*$/;
const ANCHOR = /\s*\{#([a-z0-9-]+)\}\s*$/;
const LINK = /\[[^\]]+\]\(([^)\s]+)\)/g;

export interface HelpHeading {
  level: number;
  text: string;
  anchor: string | null;
}

/** splitHeading("What it is {#what-it-is}") → { text: "What it is", anchor: "what-it-is" }. */
export function splitHeading(raw: string): { text: string; anchor: string | null } {
  const m = ANCHOR.exec(raw);
  if (!m) return { text: raw.trim(), anchor: null };
  return { text: raw.slice(0, m.index).trim(), anchor: m[1]! };
}

function lines(src: string): string[] {
  const out: string[] = [];
  let fenced = false;
  for (const line of src.replace(/\r\n?/g, "\n").split("\n")) {
    if (/^\s*(```|~~~)/.test(line)) fenced = !fenced;
    out.push(fenced ? "" : line);
  }
  return out;
}

export function headings(src: string): HelpHeading[] {
  const out: HelpHeading[] = [];
  for (const line of lines(src)) {
    const m = HEADING.exec(line);
    if (m) out.push({ level: m[1]!.length, ...splitHeading(m[2]!) });
  }
  return out;
}

/** The topic's title: its first `# ` heading. */
export function topicTitle(src: string): string {
  return headings(src).find((h) => h.level === 1)?.text ?? "";
}

export function anchors(src: string): string[] {
  return headings(src)
    .map((h) => h.anchor)
    .filter((a): a is string => a !== null);
}

/** Every link target in the topic. */
export function links(src: string): string[] {
  return [...src.matchAll(LINK)].map((m) => m[1]!);
}

export interface HelpSection {
  /** The nearest anchored heading at or above this text ("" = the top). */
  anchor: string;
  heading: string;
  /** The section's words without Markdown marks, for search and snippets. */
  text: string;
}

/** plain strips the Markdown marks search should not match on. */
export function plain(md: string): string {
  return md
    .replace(/\[([^\]]+)\]\([^)\s]+\)/g, "$1")
    .replace(/[*_`>]/g, "")
    .replace(/^\s*(?:[-+]|\d+[.)])\s+/gm, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** sections cuts the topic at each anchored heading. */
export function sections(src: string): HelpSection[] {
  const out: HelpSection[] = [];
  let cur: { anchor: string; heading: string; body: string[] } = {
    anchor: "",
    heading: "",
    body: [],
  };
  const flush = () => {
    out.push({ anchor: cur.anchor, heading: cur.heading, text: plain(cur.body.join("\n")) });
  };
  for (const line of lines(src)) {
    const m = HEADING.exec(line);
    if (m) {
      const h = splitHeading(m[2]!);
      if (m[1]!.length === 1) {
        cur.heading = h.text;
        continue;
      }
      if (h.anchor) {
        flush();
        cur = { anchor: h.anchor, heading: h.text, body: [] };
        continue;
      }
      cur.body.push(h.text);
      continue;
    }
    cur.body.push(line);
  }
  flush();
  return out.filter((s) => s.heading || s.text);
}
