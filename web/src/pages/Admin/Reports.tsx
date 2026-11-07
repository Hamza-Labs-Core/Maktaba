// Problem reports, the admin's inbox (Epic 53 story 53.15, /admin/reports): number, who,
// when, category, app, status. A report: its text and device, the timeline (the app's lines
// and the home's merged by time; a request id follows its request in Logs), and the actions:
// set its status, write back (its sender sees it in My reports), download the bundle.
import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../lib/i18n";
import { downloadBlob } from "../../lib/api";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";
import { logsPageLink } from "../../lib/logIndex";
import {
  STATUSES,
  getReport,
  listReports,
  replyToReport,
  reportBundlePath,
  setReportStatus,
  timeline,
  type ReportDoc,
  type ReportStatus,
  type ReportSummary,
} from "../../lib/problemReports";

export function AdminReports() {
  return (
    <AdminGate>
      <Inbox />
    </AdminGate>
  );
}

function Inbox() {
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
      <header>
        <TitleRow>
          <h1>{t("report.inbox")}</h1>
        </TitleRow>
      </header>
      {err && <p className="mkt-muted">{t("report.listError")}</p>}
      {list && list.length === 0 && <p className="mkt-muted">{t("report.none")}</p>}
      {list && list.length > 0 && (
        <Card>
          <table className="mkt-errors__table">
            <thead>
              <tr>
                <th>#</th>
                <th>{t("report.who")}</th>
                <th>{t("report.whenCol")}</th>
                <th>{t("report.category")}</th>
                <th>{t("report.app")}</th>
                <th>{t("report.statusCol")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((r) => (
                <tr key={r.id} data-testid="report-row">
                  <td>
                    <Link to={`/admin/reports/${r.id}`}>#{r.id}</Link>
                  </td>
                  <td>
                    <bdi>{r.name ?? "—"}</bdi>
                  </td>
                  <td>{formatDate(r.at)}</td>
                  <td>{t(`report.category.${r.category}`)}</td>
                  <td>{r.app}</td>
                  <td>
                    <Badge
                      tone={r.status === "new" ? "error" : "neutral"}
                      className="mkt-badge--sm"
                    >
                      {t(`report.status.${r.status}`)}
                    </Badge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </section>
  );
}

export function AdminReport() {
  return (
    <AdminGate>
      <ReportPage />
    </AdminGate>
  );
}

function ReportPage() {
  const { id = "" } = useParams();
  const num = Number(id);
  const { t, formatDate } = useI18n();
  const [d, setD] = useState<ReportDoc | null>(null);
  const [err, setErr] = useState(false);
  const [reply, setReply] = useState("");
  const load = useCallback(() => {
    getReport(num)
      .then((r) => {
        setD(r);
        setReply(r.reply ?? "");
      })
      .catch(() => setErr(true));
  }, [num]);
  useEffect(load, [load]);
  if (err && !d) return <p className="mkt-muted">{t("report.loadError")}</p>;
  if (!d) return null;
  return (
    <section className="mkt-report" data-testid="report-page">
      <h1>
        #{d.id} · {t(`report.category.${d.category}`)}
      </h1>
      <dl className="mkt-errors__facts">
        <dt>{t("report.who")}</dt>
        <dd>
          <bdi>{d.name ?? "—"}</bdi>
        </dd>
        <dt>{t("report.whenCol")}</dt>
        <dd>{formatDate(d.when || d.at)}</dd>
        <dt>{t("report.app")}</dt>
        <dd>
          <bdi dir="ltr">
            {d.app} {d.version} · {d.platform} {d.device_model ?? ""}
          </bdi>
        </dd>
      </dl>
      {d.text && <blockquote className="mkt-report__said">{d.text}</blockquote>}
      <div className="mkt-errors__actions">
        <label>
          {t("report.statusCol")}{" "}
          <Select
            value={d.status}
            onChange={(e) =>
              setReportStatus(num, e.target.value as ReportStatus)
                .then(load)
                .catch(() => setErr(true))
            }
          >
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`report.status.${s}`)}
              </option>
            ))}
          </Select>
        </label>
        <Button
          variant="secondary"
          onClick={() =>
            downloadBlob(reportBundlePath(num), `maktaba-report-${num}.tar.gz`).catch(() =>
              setErr(true)
            )
          }
        >
          {t("report.download")}
        </Button>
      </div>
      <Card>
        <label className="mkt-report__field">
          <span>{t("report.reply")}</span>
          <textarea
            rows={3}
            maxLength={1000}
            value={reply}
            onChange={(e) => setReply(e.target.value)}
          />
        </label>
        <Button
          onClick={() =>
            replyToReport(num, reply)
              .then(load)
              .catch(() => setErr(true))
          }
        >
          {t("report.sendReply")}
        </Button>
      </Card>
      <h2>{t("report.timeline")}</h2>
      <Card className="mkt-logs__viewport">
        <ul className="mkt-logs__list" data-testid="report-timeline">
          {timeline(d).map((l, i) => (
            <li key={i} className={`mkt-logs__row mkt-report__${l.side}`}>
              <span className="mkt-logs__ts">{l.ts ? formatDate(l.ts) : "—"}</span>
              <span className="mkt-logs__service">
                {l.side === "app" ? t("report.side.app") : l.service}
              </span>
              <span className="mkt-logs__msg" dir="ltr">
                {l.msg}
              </span>
              {l.request_id && (
                <Link className="mkt-logs__chip" to={logsPageLink({ request: l.request_id })}>
                  {t("logs.chip.request")}: <bdi dir="ltr">{l.request_id.slice(0, 8)}</bdi>
                </Link>
              )}
            </li>
          ))}
        </ul>
      </Card>
    </section>
  );
}
