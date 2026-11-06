// Fixing a word from the transcript tab (Epic 52, story 52.12): in edit mode a sentence's
// words (Intl.Segmenter, UAX #29; whitespace without it) can be selected, one with a click
// and a span with Shift+click, within the sentence. The popover saves a new spelling, a
// name or a dictionary word through POST /api/videos/{id}/transcript/words, sending the
// span in code points (the edit contract) with the text it read, so a sentence changed
// meanwhile is refused (409 stale) and reloaded, never overwritten.
import { useMemo, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import { toCodePoints } from "../../lib/codepoints";
import { reviewApi } from "../../lib/reviewApi";

export interface WordSpan {
  start: number; // UTF-16
  end: number;
  text: string;
  word: boolean;
}

/** The sentence's pieces, words marked (offsets in UTF-16 units). */
export function segmentWords(text: string, lang: string): WordSpan[] {
  const Seg = (Intl as unknown as { Segmenter?: typeof Intl.Segmenter }).Segmenter;
  if (Seg) {
    const out: WordSpan[] = [];
    for (const s of new Seg(lang || undefined, { granularity: "word" }).segment(text)) {
      out.push({
        start: s.index,
        end: s.index + s.segment.length,
        text: s.segment,
        word: !!s.isWordLike,
      });
    }
    return out;
  }
  const out: WordSpan[] = [];
  const re = /[^\s.,!?;:،؛؟()«»"]+|[\s.,!?;:،؛؟()«»"]+/gu;
  for (const m of text.matchAll(re)) {
    out.push({
      start: m.index ?? 0,
      end: (m.index ?? 0) + m[0].length,
      text: m[0],
      word: !/^[\s\p{P}]+$/u.test(m[0]),
    });
  }
  return out;
}

/** A UTF-16 selection [start, end) as the edit API wants it: code points. */
export function spanToCodePoints(
  text: string,
  start: number,
  end: number
): { char_start: number; char_len: number } {
  const a = toCodePoints(text, start);
  return { char_start: a, char_len: toCodePoints(text, end) - a };
}

export function WordPicker({
  videoId,
  seq,
  text,
  lang,
  onSaved,
  onStale,
  onUndoable,
}: {
  videoId: string;
  seq: number;
  text: string;
  lang: string;
  onSaved: (text: string) => void;
  onStale: () => void;
  onUndoable: (editId: string, label: string) => void;
}) {
  const { t } = useI18n();
  const parts = useMemo(() => segmentWords(text, lang), [text, lang]);
  const [sel, setSel] = useState<[number, number] | null>(null); // part indices
  const [value, setValue] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const pick = (i: number, extend: boolean) => {
    if (!parts[i].word) return;
    const next: [number, number] =
      extend && sel ? [Math.min(sel[0], i), Math.max(sel[1], i)] : [i, i];
    setSel(next);
    setValue(text.slice(parts[next[0]].start, parts[next[1]].end));
    setMsg(null);
  };
  const save = async (action: "correct" | "name" | "dictionary") => {
    if (!sel) return;
    const start = parts[sel[0]].start;
    const end = parts[sel[1]].end;
    const before = text.slice(start, end);
    try {
      const res = await reviewApi.fixWord(videoId, {
        segment_seq: seq,
        ...spanToCodePoints(text, start, end),
        before,
        text: action === "correct" ? value.trim() : value.trim() || before,
        action,
        language: lang,
      });
      onSaved(res.text);
      if (res.edit_id)
        onUndoable(res.edit_id, t("review.fixed", { from: before, to: value.trim() }));
      setSel(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        setMsg(t("transcript.edit.stale"));
        onStale();
      } else setMsg(t("review.actionFailed"));
    }
  };
  return (
    <>
      <span dir="auto">
        {parts.map((p, i) =>
          p.word ? (
            <span
              key={i}
              role="button"
              tabIndex={0}
              className="mkt-transcript-word"
              aria-selected={sel !== null && i >= sel[0] && i <= sel[1]}
              onClick={(e) => pick(i, e.shiftKey)}
              onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && pick(i, e.shiftKey)}
            >
              {p.text}
            </span>
          ) : (
            <span key={i}>{p.text}</span>
          )
        )}
      </span>
      {sel && (
        <span className="mkt-transcript-pop" role="group" aria-label={t("transcript.edit.popover")}>
          <label>
            {t("review.editLabel")}
            <input value={value} onChange={(e) => setValue(e.target.value)} dir="auto" autoFocus />
          </label>
          <span className="mkt-review-actions">
            <button type="button" onClick={() => void save("correct")} disabled={!value.trim()}>
              {t("transcript.edit.save")}
            </button>
            <button type="button" onClick={() => void save("name")}>
              {t("review.name")}
            </button>
            <button type="button" onClick={() => void save("dictionary")}>
              {t("review.dictionary")}
            </button>
            <button type="button" onClick={() => setSel(null)}>
              {t("transcript.edit.cancel")}
            </button>
          </span>
          {msg && <span role="status">{msg}</span>}
        </span>
      )}
    </>
  );
}
