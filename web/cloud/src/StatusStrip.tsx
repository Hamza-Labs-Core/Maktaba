// The cloud web app's status strip (Epic 53 story 53.06 §3, fed by Epic 50 story 50.11): one
// line when Maktaba Cloud's relay or sign-in is down or slow, or maintenance is within 24
// hours, from https://status.maktaba.tv/summary.json, with Details to the status page. It
// never blocks: no answer, no line. Closed, it stays closed for that incident.
import { useState } from "react";
import { cloudLine, useCloudStatus } from "../../src/lib/cloudStatus";
import { useI18n } from "./i18n";

export function StatusStrip() {
  const { t, locale } = useI18n();
  const summary = useCloudStatus(true);
  const [closed, setClosed] = useState("");
  const intl = locale === "ar" ? "ar-EG" : "en";
  const line = cloudLine(summary, Date.now(), {
    date: (d) => new Intl.DateTimeFormat(intl, { dateStyle: "medium" }).format(d),
    time: (d) => new Intl.DateTimeFormat(intl, { timeStyle: "short" }).format(d),
  });
  if (!line || closed === line.key) return null;
  return (
    <div
      className="mkc-status-strip"
      role="status"
      aria-live="polite"
      data-testid="cloud-status-strip"
    >
      <span>{t(line.text, line.vars)}</span>{" "}
      <a href={line.href} target="_blank" rel="noopener noreferrer">
        {t("status.details")}
      </a>
      <button
        type="button"
        className="mkc-status-strip__close"
        aria-label={t("status.close")}
        onClick={() => setClosed(line.key)}
      >
        ×
      </button>
    </div>
  );
}
