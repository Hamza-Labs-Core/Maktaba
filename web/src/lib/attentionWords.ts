// What a "Needs attention" group says. Its cause is a grouping key, not words: a cause the
// engine could not name is "kind:normalised message", the message's numbers made <n> and
// its paths <path> (pipeline engine/causes.py). The title is the cause's words, by its code
// or its kind, in the page's language (`queue.issues.cause.<code>`, the same words as the
// Android app's AttentionWords.kt and the Apple apps' AttentionWords.swift); the home's
// label (api queues/causes.go) only for a code the web has no words for.

/** The cause codes with words (and the kinds the stages raise that are no code of their own). */
export const CAUSE_CODES = [
  "nas_timeout",
  "nas_invalid_argument",
  "nas_partial_read",
  "file_missing",
  "ffmpeg_decode",
  "youtube_bot_check",
  "youtube_rate_limited",
  "youtube_unavailable",
  "plex_unreachable",
  "plex_auth",
  "dizquetv_unreachable",
  "readback_mismatch",
  "no_handler",
  "graph_violation",
  "db_error",
  "shm_full",
  "disk_full",
  "permission_denied",
  "ffmpeg_rc",
  "awaiting_review",
  "stale_graph",
  "model_unavailable",
  "thumbnail_unreadable",
  "video_unhashed",
  "unknown",
] as const;

/** Codes that say what another code says (a chapter's frames fail as ffmpeg does). */
const SAME_WORDS: Record<string, string> = { chapter_frames_failed: "ffmpeg_rc" };

const HAS_WORDS = new Set<string>(CAUSE_CODES);

function wordsKey(code: string): string | null {
  const c = SAME_WORDS[code] ?? code;
  return HAS_WORDS.has(c) ? `queue.issues.cause.${c}` : null;
}

/** A generated cause's kind ("nas_timeout:<path>: no answer" is nas_timeout's), else the cause. */
export function causeKind(cause: string): string {
  const i = cause.indexOf(":");
  return i > 0 ? cause.slice(0, i) : cause;
}

/** True when s is a grouping key, never words to show: a code's "kind:..." or a placeholder. */
export function looksLikeKey(s: string): boolean {
  return /^[a-z][a-z0-9_]*:/.test(s) || /<(n|path|id|hex)>/.test(s);
}

/** A group's title: its cause's words, else its kind's, else the home's label, else the kind in words. */
export function groupTitle(
  g: { cause: string; label: string },
  t: (key: string) => string
): string {
  const key = wordsKey(g.cause) ?? wordsKey(causeKind(g.cause));
  if (key) return t(key);
  if (g.label.trim() !== "" && !looksLikeKey(g.label)) return g.label;
  const spaced = causeKind(g.cause).replace(/_/g, " ").trim();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

const FSI = "⁨";
const PDI = "⁩";
// right-to-left letters: Hebrew, Arabic, Syriac, Thaana, N'Ko ... and their presentation forms
const RTL = /[֐-ࣿיִ-﷿ﹰ-﻿]|[\u{10800}-\u{10FFF}\u{1E800}-\u{1EFFF}]/u;

/**
 * Each part of line between ": " separators that has right-to-left letters, set apart in its
 * own direction (Unicode's first-strong isolate): "<file>: no answer" with an Arabic file
 * name reads as the name, then ": no answer", in an English line or an Arabic one.
 */
export function isolateRtl(line: string): string {
  if (!RTL.test(line)) return line;
  return line
    .split(": ")
    .map((part) => (RTL.test(part) ? `${FSI}${part}${PDI}` : part))
    .join(": ");
}

/**
 * A job's message as it should read: a repr's escapes as real line breaks (never a
 * backslash-n), blank lines dropped, and each part in its own direction (isolateRtl).
 * Shown with `white-space: pre-line` so the breaks are lines.
 */
export function readableMessage(message: string): string {
  return message
    .replace(/\\r\\n/g, "\n")
    .replace(/\\n/g, "\n")
    .replace(/\\r/g, "\n")
    .replace(/\\t/g, " ")
    .split(/\r\n|\r|\n/)
    .map((l) => l.trimEnd())
    .filter((l) => l.trim() !== "")
    .map(isolateRtl)
    .join("\n")
    .trim();
}
