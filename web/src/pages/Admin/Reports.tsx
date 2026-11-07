// Problem reports, the admin's inbox (Epic 53 story 53.15, /admin/reports): number, who,
// when, category, app, status. A report: its text and device, the timeline (the app's lines
// and the home's merged by time; a request id follows its request in Logs), and the actions:
// set its status, write back (its sender sees it in My reports), download the bundle, and
// send it to Maktaba (story 53.16) with the admin's own preview of what leaves the home.
import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../lib/i18n";
import { apiUrl, downloadBlob } from "../../lib/api";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";
import { logsPageLink } from "../../lib/logIndex";
import { MaktabaStatus } from "../Profile/MaktabaStatus";
import {
  STATUSES,
  getReport,
  listReports,
  maktabaPreview,
  maktabaSetting,
  shareReport,
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
      <MaktabaStatus m={d.maktaba} />
      <SendToMaktaba d={d} onSent={load} />
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

/** States a report can be sent again from (it never went, or did not arrive). */
const SENDABLE = new Set(["expired", "refused", "cancelled"]);

// Send to Maktaba (story 53.16): shown while the home lets reports go to Maktaba, for a
// report that is not on its way. The admin first sees exactly what leaves the home, and its
// screenshot goes only when the admin ticks it.
function SendToMaktaba({ d, onSent }: { d: ReportDoc; onSent: () => void }) {
  const { t } = useI18n();
  const [on, setOn] = useState(false);
  const [preview, setPreview] = useState<string | null>(null);
  const [shot, setShot] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(false);
  useEffect(() => {
    maktabaSetting()
      .then((s) => setOn(s?.enabled === true))
      .catch(() => setOn(false));
  }, []);
  if (!on || (d.maktaba && !SENDABLE.has(d.maktaba.state))) return null;
  return (
    <Card data-testid="report-send-maktaba">
      {preview === null ? (
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            maktabaPreview(d.id)
              .then((p) => setPreview(JSON.stringify(p, null, 2)))
              .catch(() => setErr(true))
          }
        >
          {t("report.shareMaktaba")}
        </Button>
      ) : (
        <>
          <p className="mkt-report__label">{t("report.shareMaktaba.preview")}</p>
          <pre className="mkt-report__lines" dir="ltr" data-testid="report-maktaba-preview">
            {preview}
          </pre>
          {d.screenshot && (
            <img
              className="mkt-report__shot"
              src={apiUrl(`/api/reports/${d.id}/screenshot`)}
              alt={t("report.shareMaktaba.screenshotAlt")}
              data-testid="report-maktaba-shot"
            />
          )}
          {d.screenshot && (
            <label>
              <input type="checkbox" checked={shot} onChange={(e) => setShot(e.target.checked)} />{" "}
              {t("report.shareMaktaba.screenshot")}
            </label>
          )}
          <p className="mkt-muted">{t("report.to.maktaba.note")}</p>
          <Button
            disabled={busy}
            onClick={() => {
              setBusy(true);
              shareReport(d.id, shot)
                .then(onSent)
                .catch(() => setErr(true))
                .finally(() => setBusy(false));
            }}
          >
            {t("report.shareMaktaba.send")}
          </Button>
        </>
      )}
      {err && (
        <p role="alert" className="mkt-muted">
          {t("report.shareMaktaba.error")}
        </p>
      )}
    </Card>
  );
}
