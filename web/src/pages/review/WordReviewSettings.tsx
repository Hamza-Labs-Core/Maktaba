// A library's word review settings (Epic 52, story 52.14), in Manage libraries: on/off,
// the backfill of its older transcripts with its progress, sensitivity, the budget, the
// reasons and a note for reviewers. A key not set takes the home's default.
import { useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import {
  reviewApi,
  SWITCHABLE,
  type WordReviewSettings as S,
  type WordReviewView,
} from "../../lib/reviewApi";

export function WordReviewSettings({ libraryId }: { libraryId: string }) {
  const { t, n } = useI18n();
  const [view, setView] = useState<WordReviewView | null>(null);
  const [s, setS] = useState<S>({});
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => {
    void reviewApi
      .settings(libraryId)
      .then((v) => {
        setView(v);
        setS(v.settings ?? {});
      })
      .catch(() => setView(null));
  }, [libraryId]);
  if (!view) return null;
  const save = async () => {
    try {
      await reviewApi.saveSettings(libraryId, s);
      setMsg(t("wordReview.settings.saved"));
    } catch (e) {
      const field =
        e instanceof ApiError ? (e.problem as unknown as { field?: string }).field : undefined;
      setMsg(field ? t("wordReview.settings.invalid", { field }) : t("review.actionFailed"));
    }
  };
  const b = view.backfill;
  return (
    <section className="mkt-wordreview-settings" aria-labelledby="wr-title">
      <h2 id="wr-title">{t("wordReview.settings.title")}</h2>
      <label>
        <input
          type="checkbox"
          checked={s.enabled ?? true}
          onChange={(e) => setS({ ...s, enabled: e.target.checked })}
        />
        {t("wordReview.settings.enabled")}
      </label>
      <label>
        <input
          type="checkbox"
          checked={s.backfill ?? true}
          onChange={(e) => setS({ ...s, backfill: e.target.checked })}
        />
        {t("wordReview.settings.backfill")}
      </label>
      <p>
        {t("wordReview.settings.progress", {
          checked: n(b.checked),
          total: n(b.transcripts),
          hours: n(Math.round(b.audio_hours)),
          cpu: n(Math.round(b.cpu_hours_left * 10) / 10),
        })}
      </p>
      <label>
        {t("wordReview.settings.sensitivity")}
        <select
          value={s.sensitivity ?? "normal"}
          onChange={(e) => setS({ ...s, sensitivity: e.target.value as S["sensitivity"] })}
        >
          {(["low", "normal", "high"] as const).map((v) => (
            <option key={v} value={v}>
              {t(`wordReview.settings.sensitivities.${v}`)}
            </option>
          ))}
        </select>
      </label>
      <label>
        {t("wordReview.settings.budget")}
        <input
          type="number"
          min={10}
          max={200}
          value={s.budget_per_hour ?? 40}
          onChange={(e) => setS({ ...s, budget_per_hour: Number(e.target.value) })}
        />
      </label>
      <fieldset>
        <legend>{t("wordReview.settings.reasons")}</legend>
        {SWITCHABLE.map((r) => (
          <label key={r}>
            <input
              type="checkbox"
              checked={s.reasons?.[r] ?? true}
              onChange={(e) =>
                setS({ ...s, reasons: { ...(s.reasons ?? {}), [r]: e.target.checked } })
              }
            />
            {t(`wordReview.settings.reason.${r}`)}
          </label>
        ))}
      </fieldset>
      <label>
        {t("wordReview.settings.note")}
        <textarea
          value={s.reviewers_note ?? ""}
          onChange={(e) => setS({ ...s, reviewers_note: e.target.value })}
          dir="auto"
        />
      </label>
      <button type="button" onClick={() => void save()}>
        {t("wordReview.settings.save")}
      </button>
      {msg && <p role="status">{msg}</p>}
    </section>
  );
}
