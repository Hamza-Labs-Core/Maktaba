// Settings → Privacy → Usage data: this server (story 16.5), an admin's. The
// switch (runtime setting telemetry.share_server_data, off until an admin turns
// it on) shares the server's own report with Maktaba once a day. It answers for
// the server only: each app asks its own user, and no viewer is asked anything.
// The admin sees exactly what the day's report holds before agreeing, how the
// delivery goes, and can ask Maktaba to delete what the server sent.
//
//   GET   /api/telemetry/server          { sharing, enabled, pending, last_sent_at, last_error, preview }
//   PATCH /api/settings                  { "telemetry.share_server_data": bool }
//   POST  /api/telemetry/server/forget
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

export const SHARE_SERVER_DATA = "telemetry.share_server_data";

export interface ServerUsageStatus {
  sharing: boolean;
  enabled: boolean;
  pending: number;
  oldest_pending: string | null;
  last_sent_at: string | null;
  last_error: string;
  preview: Record<string, unknown>;
}

export function ServerUsageData() {
  const { t, n, formatDate } = useI18n();
  const toast = useToast();
  const [status, setStatus] = useState<ServerUsageStatus | null>(null);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);

  async function load() {
    try {
      setStatus(await api.get<ServerUsageStatus>("/api/telemetry/server"));
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  function problem(e: unknown) {
    toast.show({
      tone: "error",
      message: e instanceof ApiError ? e.problem.title : t("common.error"),
    });
  }

  async function toggle() {
    if (!status) return;
    setBusy(true);
    try {
      await api.patch("/api/settings", { [SHARE_SERVER_DATA]: !status.sharing });
      toast.show({ tone: "success", message: t("telemetry.server.saved") });
      await load();
    } catch (e) {
      problem(e);
    } finally {
      setBusy(false);
    }
  }

  async function forget() {
    setBusy(true);
    try {
      await api.post("/api/telemetry/server/forget", {});
      toast.show({ tone: "success", message: t("telemetry.server.forgotten") });
      await load();
    } catch (e) {
      problem(e);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mkt-telemetry-settings__server" data-testid="server-usage-data">
      <h2>{t("telemetry.server.title")}</h2>
      <p className="mkt-muted">{t("telemetry.server.hint")}</p>
      <p className="mkt-telemetry__label">{t("telemetry.collected")}</p>
      <ul>
        <li>{t("telemetry.server.bullet.version")}</li>
        <li>{t("telemetry.server.bullet.size")}</li>
        <li>{t("telemetry.server.bullet.apps")}</li>
        <li>{t("telemetry.server.bullet.features")}</li>
        <li>{t("telemetry.server.bullet.errors")}</li>
      </ul>
      <p className="mkt-telemetry__label">{t("telemetry.never")}</p>
      <ul>
        <li>{t("telemetry.bullet.never")}</li>
      </ul>
      {failed && <p role="alert">{t("telemetry.server.loadFailed")}</p>}
      {status && !status.enabled && <p role="note">{t("telemetry.server.disabled")}</p>}
      <Toggle
        checked={status?.sharing === true}
        disabled={!status || busy || !status.enabled}
        onChange={() => void toggle()}
        label={status?.sharing ? t("telemetry.server.on") : t("telemetry.server.off")}
      />
      {status && (
        <>
          <p className="mkt-muted">
            {status.last_sent_at
              ? t("telemetry.server.lastSent", { when: formatDate(status.last_sent_at) })
              : t("telemetry.server.notSent")}
            {status.pending > 0 && (
              <> · {t("telemetry.server.pending", { count: n(status.pending) })}</>
            )}
          </p>
          {status.sharing && status.last_error && (
            <p className="mkt-muted">
              {t("telemetry.server.lastError", { error: status.last_error })}
            </p>
          )}
          <details>
            <summary>{t("telemetry.server.preview")}</summary>
            <pre className="mkt-telemetry-settings__preview" dir="ltr">
              {JSON.stringify(status.preview, null, 2)}
            </pre>
          </details>
        </>
      )}
      <Button
        size="sm"
        variant="secondary"
        disabled={busy || !status}
        onClick={() => void forget()}
      >
        {t("telemetry.server.forget")}
      </Button>
    </div>
  );
}
