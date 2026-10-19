// The Checks tab (story 33.11 AC3): each verification check (verify_report.checks) per
// channel with its measured value; failures first, then warnings, passes, skipped.
import { useI18n } from "../../../lib/i18n";
import type { CheckResult, VerifyReport } from "../../../lib/tvChanges";
import { ChecksSummary, Clock, useTk } from "./changeBits";

const ORDER: Record<CheckResult, number> = { fail: 0, warn: 1, pass: 2, skip: 3 };

export function ChecksTable({
  report,
  names,
}: {
  report: VerifyReport | undefined;
  names: Record<string, string>;
}) {
  const { t } = useI18n();
  const tk = useTk();
  const checks = [...(report?.checks ?? [])]
    .map((c, i) => ({ c, i }))
    .sort((a, b) => ORDER[a.c.result] - ORDER[b.c.result] || a.i - b.i)
    .map((x) => x.c);
  if (!report || (!checks.length && !report.error)) {
    return <p className="mkt-tvx-muted">{t("tvchanges.checks.notYet")}</p>;
  }
  return (
    <div className="mkt-tvx-diff" data-testid="checks">
      <p className="mkt-tvx-checkline">
        <ChecksSummary report={report} />
        {report.at && (
          <span className="mkt-tvx-muted">
            {" · "}
            {t("tvchanges.checks.at")} <Clock at={report.at} />
          </span>
        )}
      </p>
      {report.interim && <p className="mkt-tvx-hint">{t("tvchanges.checks.interim")}</p>}
      {report.runtime === "slots" && (
        <p className="mkt-tvx-hint" data-testid="checks-slot">
          {t("tvchanges.checks.onSlot", { slot: report.slot ?? "staging" })}
          {report.status ? ` · ${report.status}` : ""}
          {report.budget
            ? ` · ${t("tvchanges.checks.budget", {
                staging: report.budget.staging,
                capacity: report.budget.capacity,
                live: report.budget.live,
                standby: report.budget.standby,
              })}`
            : ""}
        </p>
      )}
      {report.reasons && report.reasons.length > 0 && (
        <div role="alert" className="mkt-tvx-error" data-testid="checks-reasons">
          <p>{t("tvchanges.checks.reasons")}</p>
          <ul>
            {report.reasons.map((r) => (
              <li key={r} dir="auto">
                {r}
              </li>
            ))}
          </ul>
        </div>
      )}
      {report.error && (
        <p role="alert" className="mkt-tvx-error">
          {t("tvchanges.checks.error", { error: report.error })}
        </p>
      )}
      <table className="mkt-tvx-kv mkt-tvx-checktable">
        <thead>
          <tr>
            <th scope="col">{t("tvchanges.checks.result")}</th>
            <th scope="col">{t("tvchanges.checks.check")}</th>
            <th scope="col">{t("tvchanges.checks.channel")}</th>
            <th scope="col">{t("tvchanges.checks.value")}</th>
          </tr>
        </thead>
        <tbody>
          {checks.map((c, i) => (
            <tr key={`${c.check}-${c.channel_id ?? ""}-${i}`} data-result={c.result}>
              <td>
                <span className={`mkt-tvx-res mkt-tvx-res--${c.result}`}>
                  {t(`tvchanges.result.${c.result}`)}
                </span>
              </td>
              <td>
                {tk(`tvchanges.check.${c.check}`, c.check)}
                <span className="mkt-tvx-mono mkt-tvx-muted"> {c.check}</span>
              </td>
              <td>{c.channel_id ? (names[c.channel_id] ?? c.channel_id.slice(0, 8)) : "—"}</td>
              <td dir="auto">{c.value ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
