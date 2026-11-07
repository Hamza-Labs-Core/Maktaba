// My reports (Epic 53 story 53.15, /account/reports): the reports this person sent, with
// their status and the admin's reply. Never the home's side, and never anyone else's.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { useI18n } from "../../lib/i18n";
import { listReports, type ReportSummary } from "../../lib/problemReports";

export function MyReports() {
  const { t, formatDate } = useI18n();
  const [list, setList] = useState<ReportSummary[] | null>(null);
  const [err, setErr] = useState(false);
  useEffect(() => {
    listReports()
      .then(setList)
      .catch(() => setErr(true));
  }, []);
  return (
    <section className="mkt-report">
      <h1>{t("report.mine")}</h1>
      <p>
        <Link to="/account/report">{t("report.title")}</Link>
      </p>
      {err && <p className="mkt-muted">{t("report.listError")}</p>}
      {list && list.length === 0 && <p className="mkt-muted">{t("report.none")}</p>}
      {list?.map((r) => (
        <Card key={r.id} data-testid="my-report">
          <strong>#{r.id}</strong> {t(`report.category.${r.category}`)} · {formatDate(r.at)} ·{" "}
          {t(`report.status.${r.status}`)}
          {r.reply && <p data-testid="my-report-reply">{r.reply}</p>}
        </Card>
      ))}
    </section>
  );
}
