// Admin -> Updates: the home's backups (Epic 35 story 35.5 AC8). The dumps the home keeps
// (the one each update takes before its migrations, the bundled database's daily one, the
// database a failed update or a restore replaced), and putting one back: the home restarts
// on the kept release whose schema is the backup's, and that release puts the backup back
// before anything else runs. Every change made since the backup is lost, so the restore
// asks for the backup's name to be typed; the database it replaces is kept aside.
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  updatesApi,
  type HomeBackup,
  type HomeBackups,
  type RestorePlan,
} from "../../lib/updatesApi";

function size(bytes: number): string {
  if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)} GiB`;
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MiB`;
  return `${Math.ceil(bytes / 1024)} KiB`;
}

/** A refusal's words by its code (the server's English when this page lacks it). */
function refusal(
  t: (k: string, v?: Record<string, string | number>) => string,
  e: unknown
): string {
  if (e instanceof ApiError) {
    const code = e.problem.code ?? "";
    const key = `updates.backups.refused.${code}`;
    const said = t(key);
    if (code && said !== key) return e.problem.detail ? `${said} (${e.problem.detail})` : said;
    return e.problem.detail ?? e.message;
  }
  return t("common.error");
}

export function UpdatesBackups() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const [list, setList] = useState<HomeBackups | null>(null);
  const [plan, setPlan] = useState<RestorePlan | null>(null);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const l = await updatesApi.backups();
      setList(l && Array.isArray(l.backups) ? l : null);
    } catch {
      setList(null); // a home whose server cannot restore: the section is not shown
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!list) return null;

  const what = (b: HomeBackup) => {
    if (b.kind === "update")
      return t("updates.backups.kind.update", {
        from: b.from || t("updates.backups.unknownRelease"),
        to: b.to ?? "",
      });
    return t(`updates.backups.kind.${b.kind}`);
  };

  const ask = async (name: string) => {
    setBusy(true);
    try {
      setTyped("");
      setPlan(await updatesApi.restorePlan(name));
    } catch (e) {
      toast.show({ tone: "error", message: refusal(t, e) });
    } finally {
      setBusy(false);
    }
  };

  const restore = async () => {
    if (!plan) return;
    setBusy(true);
    try {
      const done = await updatesApi.restore(plan.backup);
      setPlan(null);
      toast.show({
        tone: "success",
        message: t("updates.backups.restoreDone", { release: done.release }),
      });
      await load();
    } catch (e) {
      toast.show({ tone: "error", message: refusal(t, e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <details className="mkt-updates__fold mkt-updates__backups" data-testid="updates-backups">
      <summary>
        <h2>{t("updates.backups.title")}</h2>
        <span className="mkt-muted">
          {t("updates.backups.count", { count: list.backups.length })}
        </span>
      </summary>
      <p className="mkt-muted">{t("updates.backups.intro")}</p>
      {list.restoring && (
        <p role="status" data-testid="updates-backups-restoring">
          {t("updates.backups.restoring", { name: list.restoring })}
        </p>
      )}
      {list.backups.length === 0 ? (
        <p className="mkt-muted">{t("updates.backups.none")}</p>
      ) : (
        <table className="mkt-table">
          <thead>
            <tr>
              <th scope="col">{t("updates.backups.col.taken")}</th>
              <th scope="col">{t("updates.backups.col.what")}</th>
              <th scope="col">{t("updates.backups.col.size")}</th>
              <th scope="col" aria-label={t("updates.backups.restore")} />
            </tr>
          </thead>
          <tbody>
            {list.backups.map((b) => (
              <tr key={b.name} data-testid={`updates-backup-${b.name}`}>
                <td>{formatDate(b.at)}</td>
                <td>
                  {what(b)}
                  <div className="mkt-muted" dir="ltr">
                    {b.name}
                  </div>
                </td>
                <td dir="ltr">{size(b.bytes)}</td>
                <td>
                  <Button
                    variant="secondary"
                    disabled={busy || Boolean(list.restoring)}
                    onClick={() => void ask(b.name)}
                  >
                    {t("updates.backups.restore")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <Modal
        open={plan !== null}
        onClose={() => setPlan(null)}
        title={t("updates.backups.confirmTitle")}
        footer={
          <>
            <Button variant="secondary" onClick={() => setPlan(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="primary"
              disabled={busy || !plan || typed.trim() !== plan.backup}
              onClick={() => void restore()}
            >
              {t("updates.backups.restore")}
            </Button>
          </>
        }
      >
        {plan && (
          <>
            <p data-testid="updates-backups-plan">
              {t("updates.backups.confirmBody", {
                release: plan.release,
                running: plan.running || t("updates.backups.unknownRelease"),
                at: formatDate(plan.at),
              })}
            </p>
            <Input
              label={t("updates.backups.typeName", { name: plan.backup })}
              value={typed}
              dir="ltr"
              autoComplete="off"
              onChange={(e) => setTyped(e.target.value)}
            />
          </>
        )}
      </Modal>
    </details>
  );
}
