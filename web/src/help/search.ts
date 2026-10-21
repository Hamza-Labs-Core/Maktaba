// Searching the help (Epic 41): every word of the query must appear in a
// section (its heading, its text or its topic's title). Arabic letter forms
// and diacritics fold together so "اعادة" finds "إعادة".
import { sections } from "./parse";
import type { HelpTopicId } from "./topics";

/** fold lowercases and folds Arabic letter variants, diacritics and tatweel. */
export function fold(s: string): string {
  return s
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ًͯ-ٰٟۖ-ۭـ]/g, "")
    .replace(/[إأآٱ]/g, "ا")
    .replace(/ى/g, "ي")
    .replace(/ة/g, "ه")
    .replace(/ؤ/g, "و")
    .replace(/ئ/g, "ي");
}

function terms(query: string): string[] {
  return fold(query)
    .split(/[\s,.;:!?()"'«»،؟]+/)
    .filter((w) => w.length > 0);
}

export interface HelpHit {
  topic: HelpTopicId;
  title: string;
  anchor: string;
  heading: string;
  snippet: string;
  score: number;
}

const SNIPPET_WORDS = 24;

function snippet(text: string, words: string[]): string {
  const parts = text.split(/\s+/);
  const at = parts.findIndex((p) => words.some((w) => fold(p).includes(w)));
  const start = Math.max(0, (at < 0 ? 0 : at) - 6);
  const cut = parts.slice(start, start + SNIPPET_WORDS).join(" ");
  return (start > 0 ? "… " : "") + cut + (start + SNIPPET_WORDS < parts.length ? " …" : "");
}

export function searchHelp(
  topics: ReadonlyArray<{ id: HelpTopicId; title: string; src: string }>,
  query: string,
  limit = 30
): HelpHit[] {
  const words = terms(query);
  if (words.length === 0) return [];
  const hits: HelpHit[] = [];
  for (const t of topics) {
    const title = fold(t.title);
    for (const s of sections(t.src)) {
      const heading = fold(s.heading);
      const body = fold(s.text);
      let score = 0;
      let all = true;
      for (const w of words) {
        const inHead = heading.includes(w);
        const inTitle = title.includes(w);
        const inBody = body.includes(w);
        if (!inHead && !inTitle && !inBody) {
          all = false;
          break;
        }
        score += (inHead ? 5 : 0) + (inTitle ? 2 : 0) + (inBody ? 1 : 0);
      }
      if (!all) continue;
      hits.push({
        topic: t.id,
        title: t.title,
        anchor: s.anchor,
        heading: s.heading,
        snippet: snippet(s.text, words),
        score,
      });
    }
  }
  return hits.sort((a, b) => b.score - a.score).slice(0, limit);
}
