// One suspect word (Epic 52, story 52.11): the sentence in the transcript's direction with
// the word marked (a box and an underline, not colour alone), its neighbours lighter, the
// reasons in words, the verse for a Quran mismatch with the Tanzil credit, the candidates,
// the edit field and the actions.
import type { MutableRefObject } from "react";
import { useI18n } from "../../lib/i18n";
import { cpSlice } from "../../lib/codepoints";
import type { Action, FlagDetail } from "../../lib/reviewApi";

/** The sentence cut to 25 words either side of the word (the full one on expand). */
function around(text: string, start: number, len: number): [string, string, string] {
  const chars = Array.from(text);
  const before = chars.slice(0, start).join("");
  const word = cpSlice(text, start, len);
  const after = chars.slice(start + len).join("");
  const b = before.split(/\s+/);
  const a = after.split(/\s+/);
  return [
    (b.length > 26 ? "… " : "") + b.slice(-26).join(" "),
    word,
    a.slice(0, 26).join(" ") + (a.length > 26 ? " …" : ""),
  ];
}

export function WordPanel({
  detail,
  edit,
  onEdit,
  field,
  disabled,
  onAction,
}: {
  detail: FlagDetail;
  edit: string;
  onEdit: (v: string) => void;
  field: MutableRefObject<HTMLInputElement | null>;
  disabled: boolean;
  onAction: (a: Action, text?: string) => void;
}) {
  const { t, locale } = useI18n();
  const [pre, word, post] = around(detail.context, detail.char_start, detail.char_len);
  const verse = detail.verse ?? detail.scores.quran ?? null;
  const quran = detail.reasons.includes("quran_mismatch");
  const prob = typeof detail.scores.prob === "number" ? detail.scores.prob : null;
  return (
    <div className="mkt-review-word">
      {detail.before && (
        <p className="mkt-review-ctx" dir={detail.direction}>
          {detail.before}
        </p>
      )}
      <p className="mkt-review-sentence" dir={detail.direction} lang={detail.language}>
        <bdi>{pre} </bdi>
        <mark
          aria-current="true"
          aria-label={t("review.suspect", { word })}
          className="mkt-review-mark"
        >
          {word}
        </mark>
        <bdi> {post}</bdi>
      </p>
      {detail.after && (
        <p className="mkt-review-ctx" dir={detail.direction}>
          {detail.after}
        </p>
      )}
      <ul className="mkt-review-why">
        {detail.reasons.map((r) => (
          <li key={r}>
            {r === "listeners_disagree"
              ? t("review.why.listeners_disagree", {
                  heard:
                    detail.candidates.find((c) => c.source === "second_listen")?.text ??
                    detail.original,
                })
              : r === "low_confidence" && prob !== null
                ? t("review.why.low_confidence", { p: Math.round(prob * 100) })
                : t(`review.why.${r}`, { language: detail.language })}
          </li>
        ))}
      </ul>
      {quran && verse && (
        <div className="mkt-review-verse">
          <p>{locale === "ar" ? verse.label_ar : verse.label_en}</p>
          {verse.missing ? (
            <p>{t("review.quran.missing", { word: verse.missing })}</p>
          ) : verse.kind === "ins" ? (
            <p>{t("review.quran.inserted")}</p>
          ) : (
            verse.expected && <p>{t("review.quran.expected", { word: verse.expected })}</p>
          )}
          <p className="mkt-review-credit">{t("review.quran.credit")}</p>
        </div>
      )}
      <ol className="mkt-review-candidates">
        {detail.candidates.slice(0, 5).map((c, i) => (
          <li key={`${c.text}-${i}`}>
            <button type="button" disabled={disabled} onClick={() => onAction("correct", c.text)}>
              <kbd>{i + 1}</kbd> <bdi dir="auto">{c.text}</bdi>{" "}
              <small>{t(`review.source.${c.source}`)}</small>
            </button>
          </li>
        ))}
      </ol>
      <label className="mkt-review-edit">
        {t("review.editLabel")}
        <input
          ref={field}
          dir="auto"
          value={edit}
          disabled={disabled}
          onChange={(e) => onEdit(e.target.value)}
          lang={detail.language}
        />
      </label>
      <div className="mkt-review-actions">
        <button type="button" disabled={disabled} onClick={() => onAction("accept")}>
          {t("review.accept")}
        </button>
        <button
          type="button"
          disabled={disabled || !edit.trim() || edit === detail.original}
          onClick={() => onAction("correct", edit.trim())}
        >
          {t("review.correct")}
        </button>
        <button type="button" disabled={disabled} onClick={() => onAction("name")}>
          {t("review.name")}
        </button>
        {!quran && (
          <button type="button" disabled={disabled} onClick={() => onAction("dictionary")}>
            {t("review.dictionary")}
          </button>
        )}
        <button type="button" disabled={disabled} onClick={() => onAction("skip")}>
          {t("review.skip")}
        </button>
      </div>
    </div>
  );
}
