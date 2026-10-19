// A release held for an admin's approval (Epic 35, story 35.3 AC4) on the Changes page:
// its database changes break the previous home release, so binaries cannot roll back
// and the updater does not install it by itself. Approving here is "Update now" for that
// version (POST /api/cluster/updates/run); Skip never installs it automatically.
// Renders nothing when nothing is held or the updates API is not there.
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import { updatesApi, type CatalogRelease } from "../../../lib/updatesApi";

export function HeldReleaseCard() {
  const { t } = useI18n();
  const [held, setHeld] = useState<CatalogRelease | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);

  const load = useCallback(async () => {
    try {
      const o = await updatesApi.overview();
      setHeld(o.plan.held && o.plan.target ? o.plan.target : null);
    } catch {
      setHeld(null);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!held)
    return note ? (
      <p className="mkt-muted" role="status">
        {note.text}
      </p>
    ) : null;

  const act = async (fn: () => Promise<unknown>, done: string) => {
    setBusy(true);
    try {
      await fn();
      setNote({ ok: true, text: done });
      await load();
    } catch (e) {
      setNote({ ok: false, text: e instanceof ApiError ? e.message : t("common.error") });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="mkt-tvx-banner"
      role="region"
      id="updates"
      data-testid="held-release"
      aria-label={t("updates.held.title", { version: held.version })}
    >
      <strong>{t("updates.held.title", { version: held.version })}</strong> {t("updates.held.body")}{" "}
      {held.release_url && (
        <a href={held.release_url} target="_blank" rel="noreferrer">
          {t("update.viewRelease")}
        </a>
      )}
      {note && (
        <p className={note.ok ? "mkt-muted" : "mkt-error"} role={note.ok ? "status" : "alert"}>
          {note.text}
        </p>
      )}
      <div className="mkt-tvx-actions">
        <Button
          variant="primary"
          disabled={busy}
          onClick={() =>
            act(
              () => updatesApi.runNow(held.version),
              t("updates.held.started", { version: held.version })
            )
          }
        >
          {t("updates.held.approve")}
        </Button>
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            act(
              () => updatesApi.skip(held.version),
              t("updates.held.skipped", { version: held.version })
            )
          }
        >
          {t("updates.held.skip")}
        </Button>
      </div>
    </div>
  );
}
