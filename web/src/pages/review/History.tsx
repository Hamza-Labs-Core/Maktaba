// /review/history?video= — a video's word fixes (Epic 52, story 52.9): who, when, before
// and after, and undo (any home admin may undo any fix).
import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import { reviewApi, type EditRow } from "../../lib/reviewApi";
import "../../styles/review.css";

export function ReviewHistory() {
  const { t, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const video = params.get("video") ?? "";
  const [items, setItems] = useState<EditRow[] | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [draft, setDraft] = useState(video);

  const load = useCallback(async () => {
    if (!video) return;
    try {
      setItems((await reviewApi.history(video)).items);
    } catch (e) {
      setMsg(
        e instanceof ApiError && e.status === 403 ? t("review.adminsOnly") : t("review.loadFailed")
      );
    }
  }, [video, t]);
  useEffect(() => void load(), [load]);

  const undo = async (e: EditRow) => {
    try {
      await (e.batch_id ? reviewApi.undoBatch(e.batch_id) : reviewApi.undoEdit(e.id));
      setMsg(t("review.undone"));
      await load();
    } catch (err) {
      const code = err instanceof ApiError ? err.problem.code : "";
      setMsg(code === "changed_since" ? t("review.history.changedSince") : t("review.undoFailed"));
    }
  };

  return (
    <main className="mkt-review" aria-labelledby="hist-title">
      <h1 id="hist-title">{t("review.history.title")}</h1>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setParams({ video: draft.trim() });
        }}
      >
        <label>
          {t("review.history.video")}
          <input value={draft} onChange={(e) => setDraft(e.target.value)} dir="ltr" />
        </label>
        <button type="submit">{t("review.history.show")}</button>
      </form>
      {msg && <p role="status">{msg}</p>}
      {items && items.length === 0 && <p>{t("review.history.none")}</p>}
      {items && items.length > 0 && (
        <table className="mkt-review-table">
          <thead>
            <tr>
              <th>{t("review.history.when")}</th>
              <th>{t("review.history.who")}</th>
              <th>{t("review.history.before")}</th>
              <th>{t("review.history.after")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((e) => (
              <tr key={e.id}>
                <td>{formatDate(new Date(e.created_at))}</td>
                <td>{e.user ?? (e.carried_from ? t("review.history.carried") : "")}</td>
                <td>
                  <bdi dir="auto">{e.before}</bdi>
                </td>
                <td>
                  <bdi dir="auto">{e.after}</bdi>
                </td>
                <td>
                  {e.undone_at ? (
                    t("review.history.undoneAt", { when: formatDate(new Date(e.undone_at)) })
                  ) : e.active ? (
                    <button type="button" onClick={() => void undo(e)}>
                      {t("review.undo")}
                    </button>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  );
}
