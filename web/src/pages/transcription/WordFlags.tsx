// The Transcription page's word review line (Epic 52, story 52.14): open suspect words per
// library, flags and fixes this week, and the share resolved as "accepted as is" (the live
// precision: a high share means the sensitivity asks about too many right words). Home
// admins only, like the page; nothing shows when the summary cannot be read.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { reviewApi, type Summary } from "../../lib/reviewApi";

export function WordFlags() {
  const { t, n } = useI18n();
  const [s, setS] = useState<Summary | null>(null);
  useEffect(() => {
    void reviewApi.summary().then(setS, () => setS(null));
  }, []);
  if (!s) return null;
  return (
    <section className="mkt-tr__flags" aria-labelledby="mkt-tr-flags">
      <h2 id="mkt-tr-flags">{t("transcription.flags.title")}</h2>
      <p>
        <Link to="/review">{t("transcription.flags.open", { count: n(s.open) })}</Link>
      </p>
      {s.libraries.length > 0 && (
        <table className="mkt-review-table">
          <thead>
            <tr>
              <th>{t("transcription.flags.library")}</th>
              <th>{t("transcription.flags.openCol")}</th>
              <th>{t("transcription.flags.week")}</th>
              <th>{t("transcription.flags.fixes")}</th>
              <th>{t("transcription.flags.accepted")}</th>
            </tr>
          </thead>
          <tbody>
            {s.libraries.map((l) => (
              <tr key={l.library_id}>
                <td>
                  <Link to={`/review?library=${l.library_id}`}>{l.name}</Link>
                </td>
                <td>{n(l.open)}</td>
                <td>{n(l.flags_week)}</td>
                <td>{n(l.fixes_week)}</td>
                <td>{l.resolved ? `${n(Math.round(l.accepted_share * 100))}%` : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {Object.keys(s.languages).length > 0 && (
        <p>
          {Object.entries(s.languages)
            .map(([lang, c]) => `${lang}: ${n(c)}`)
            .join(" · ")}
        </p>
      )}
    </section>
  );
}
