// Diagnostics settings (Epic 53 story 53.11, /admin/logs/settings): how long logs, error
// groups and problem reports are kept and how much disk they may take in all (7, 30 and 90
// days and 1 GB on a new home), with what each uses now; and detailed logs for one hour per
// service, which end by themselves. The supervisor keeps both (lib/logIndex.ts).
import { useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import { useToast } from "@ds/components/Toast/Toast";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";
import { useBytes } from "../../components/queue/offline";
import {
  getRetention,
  LEVEL_SERVICES,
  putRetention,
  setDetailedLogs,
  type Retention,
  type RetentionChange,
} from "../../lib/logIndex";

const GB = 1 << 30;

export function AdminLogSettings() {
  return (
    <AdminGate>
      <SettingsInner />
    </AdminGate>
  );
}

function SettingsInner() {
  const { t, formatDate } = useI18n();
  const bytes = useBytes();
  const toast = useToast();
  const [r, setR] = useState<Retention | null>(null);
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const take = (x: Retention) => {
    setR(x);
    setDraft({
      logs_days: String(x.logs_days),
      errors_days: String(x.errors_days),
      reports_days: String(x.reports_days),
      total_gb: String(Math.round((x.total_bytes / GB) * 100) / 100),
    });
  };
  useEffect(() => {
    getRetention()
      .then((x) => {
        // an answer that is not the settings document (an older server) is not shown
        if (!x || typeof x.logs_days !== "number" || !x.used || !x.ranges)
          throw new Error("no settings");
        take({ ...x, detailed: x.detailed ?? {} });
      })
      .catch(() => setErr(t("logs.settings.loadError")));
  }, [t]);

  const save = () => {
    const ch: RetentionChange = {
      logs_days: Number(draft.logs_days),
      errors_days: Number(draft.errors_days),
      reports_days: Number(draft.reports_days),
      total_bytes: Math.round(Number(draft.total_gb) * GB),
    };
    setBusy(true);
    putRetention(ch)
      .then((x) => {
        take(x);
        setErr("");
        toast.show({ tone: "success", message: t("logs.settings.saved") });
      })
      .catch((e: unknown) =>
        setErr(
          e instanceof ApiError && e.status === 400
            ? t("logs.settings.outOfRange")
            : t("logs.settings.saveError")
        )
      )
      .finally(() => setBusy(false));
  };

  const detail = (service: string, on: boolean) => {
    setDetailedLogs(service, on)
      .then(take)
      .catch(() => setErr(t("logs.settings.saveError")));
  };

  if (!r) return err ? <p className="mkt-muted">{err}</p> : null;
  const used = r.used.logs + r.used.index + r.used.reports;
  const field = (key: string, label: string, min: number, max: number, step = 1) => (
    <label className="mkt-logset__field">
      <span>{label}</span>
      <Input
        type="number"
        min={min}
        max={max}
        step={step}
        value={draft[key] ?? ""}
        onChange={(e) => setDraft((d) => ({ ...d, [key]: e.target.value }))}
        aria-label={label}
      />
    </label>
  );
  const range = (k: string) => r.ranges[k] ?? [1, 365];
  return (
    <section className="mkt-logset">
      <header>
        <TitleRow>
          <h1>{t("logs.settings.title")}</h1>
        </TitleRow>
      </header>
      <Card>
        <h2>{t("logs.settings.retention")}</h2>
        {field(
          "logs_days",
          t("logs.settings.keepLogs", { days: draft.logs_days ?? "" }),
          range("logs_days")[0],
          range("logs_days")[1]
        )}
        <p className="mkt-muted">{bytes(r.used.logs + r.used.index)}</p>
        {field(
          "errors_days",
          t("logs.settings.keepErrors", { days: draft.errors_days ?? "" }),
          range("errors_days")[0],
          range("errors_days")[1]
        )}
        {field(
          "reports_days",
          t("logs.settings.keepReports", { days: draft.reports_days ?? "" }),
          range("reports_days")[0],
          range("reports_days")[1]
        )}
        <p className="mkt-muted">{bytes(r.used.reports)}</p>
        {field(
          "total_gb",
          t("logs.settings.total", { size: bytes(Number(draft.total_gb || 0) * GB) }),
          range("total_bytes")[0] / GB,
          range("total_bytes")[1] / GB,
          0.25
        )}
        <p data-testid="logset-used">
          {t("logs.settings.used", { used: bytes(used), size: bytes(r.total_bytes) })}
        </p>
        <p className="mkt-muted">{t("logs.settings.capNote")}</p>
        {err && (
          <p className="mkt-muted" role="alert">
            {err}
          </p>
        )}
        <Button onClick={save} disabled={busy}>
          {t("common.save")}
        </Button>
      </Card>
      <Card>
        <h2>{t("logs.settings.debug")}</h2>
        <ul className="mkt-logset__services">
          {LEVEL_SERVICES.map((s) => {
            const until = r.detailed[s];
            return (
              <li key={s}>
                <span>{s}</span>{" "}
                {until ? (
                  <>
                    <span className="mkt-muted">
                      {t("logs.settings.debugUntil", { time: formatDate(until) })}
                    </span>{" "}
                    <Button variant="secondary" size="sm" onClick={() => detail(s, false)}>
                      {t("logs.settings.debugStop")}
                    </Button>
                  </>
                ) : (
                  <Button variant="secondary" size="sm" onClick={() => detail(s, true)}>
                    {t("logs.settings.debug")}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      </Card>
    </section>
  );
}
