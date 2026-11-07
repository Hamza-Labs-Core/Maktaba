// A report's way to Maktaba (Epic 53 story 53.16), as its sender (My reports) and the admins
// see it: "Also sent to Maktaba", waiting, not sent and why, deleted at Maktaba, and
// Maktaba's reply with the version a fix is in.
import { useI18n } from "../../lib/i18n";
import type { ToMaktaba } from "../../lib/problemReports";

export function MaktabaStatus({ m }: { m?: ToMaktaba | null }) {
  const { t } = useI18n();
  if (!m) return null;
  return (
    <div className="mkt-report__maktaba" data-testid="report-maktaba">
      <p className="mkt-muted">
        {m.state === "sent" ? t("report.sentToMaktaba") : t(`report.maktaba.state.${m.state}`)}
      </p>
      {m.reply && (
        <p data-testid="report-maktaba-reply">
          <strong>{t("report.maktabaReplied")}:</strong> {m.reply}
          {m.fixed_in && <> · {t("report.maktaba.fixedIn", { version: m.fixed_in })}</>}
        </p>
      )}
    </div>
  );
}
