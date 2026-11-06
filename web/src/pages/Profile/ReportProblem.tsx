// Report a problem (Epic 53 story 53.15, /account/report): what happened (optional) and a
// category, when it happened, and always what will be sent (the app and its version, the
// app's lines of the last 30 minutes with a preview, the last requests). The home adds its
// side. Sent, it shows the report's number. An error screen's "Report this" opens it with
// that error's reference (?ref=).
import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../lib/i18n";
import { logAppVersion } from "../../lib/errorLog";
import { lastRequests } from "../../lib/requestIds";
import { CATEGORIES, reportLines, sendReport, type Category } from "../../lib/problemReports";

export function ReportProblem() {
  const { t, n } = useI18n();
  const [params] = useSearchParams();
  const ref = params.get("ref") ?? undefined;
  const [category, setCategory] = useState<Category>("other");
  const [text, setText] = useState("");
  const [earlier, setEarlier] = useState(false);
  const [time, setTime] = useState("");
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<number | null>(null);
  const [err, setErr] = useState(false);
  const lines = useMemo(() => reportLines(), []);
  const requests = useMemo(() => lastRequests(), []);

  const send = () => {
    let when: string | undefined;
    if (earlier && /^\d{2}:\d{2}$/.test(time)) {
      const d = new Date();
      const [h, m] = time.split(":").map(Number);
      d.setHours(h, m, 0, 0);
      when = d.toISOString();
    }
    setBusy(true);
    sendReport({ category, text, when, appVersion: logAppVersion(), requestRef: ref })
      .then((id) => {
        setSent(id);
        setErr(false);
      })
      .catch(() => setErr(true))
      .finally(() => setBusy(false));
  };

  if (sent !== null) {
    return (
      <section className="mkt-report">
        <h1>{t("report.title")}</h1>
        <p role="status" data-testid="report-sent">
          {t("report.sent", { id: sent })}
        </p>
        <Link to="/account/reports">{t("report.mine")}</Link>
      </section>
    );
  }
  return (
    <section className="mkt-report">
      <h1>{t("report.title")}</h1>
      <Card>
        <label className="mkt-report__field">
          <span>{t("report.category")}</span>
          <Select value={category} onChange={(e) => setCategory(e.target.value as Category)}>
            {CATEGORIES.map((c) => (
              <option key={c} value={c}>
                {t(`report.category.${c}`)}
              </option>
            ))}
          </Select>
        </label>
        <label className="mkt-report__field">
          <span>{t("report.what")}</span>
          <textarea
            className="mkt-report__text"
            maxLength={2000}
            rows={5}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </label>
        <fieldset className="mkt-report__field">
          <legend>{t("report.when")}</legend>
          <label>
            <input type="radio" checked={!earlier} onChange={() => setEarlier(false)} />{" "}
            {t("report.justNow")}
          </label>
          <label>
            <input type="radio" checked={earlier} onChange={() => setEarlier(true)} />{" "}
            {t("report.earlierToday")}
          </label>
          {earlier && (
            <input
              type="time"
              aria-label={t("report.time")}
              value={time}
              onChange={(e) => setTime(e.target.value)}
            />
          )}
        </fieldset>
        <details
          open={open}
          onToggle={(e) => setOpen((e.target as HTMLDetailsElement).open)}
          data-testid="report-preview"
        >
          <summary>{t("report.whatIsSent")}</summary>
          <ul>
            <li>{t("report.sendsApp", { version: logAppVersion() })}</li>
            <li>{t("report.sendsLines", { count: n(lines.length) })}</li>
            <li>{t("report.sendsRequests", { count: n(requests.length) })}</li>
            {ref && <li>{t("report.sendsRef", { ref })}</li>}
          </ul>
          <pre className="mkt-report__lines" dir="ltr">
            {lines
              .slice(-200)
              .map((l) => JSON.stringify(l))
              .join("\n")}
          </pre>
        </details>
        <p className="mkt-muted">{t("report.toHome")}</p>
        {err && (
          <p role="alert" className="mkt-muted">
            {t("report.error")}
          </p>
        )}
        <Button onClick={send} disabled={busy}>
          {t("report.send")}
        </Button>
      </Card>
    </section>
  );
}
