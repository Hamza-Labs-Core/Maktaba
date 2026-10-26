// Settings → Advanced → Experiments (Epic 16 Story 16.6): the beta cohorts the
// signed-in user may join themself (GET /api/me/cohorts). Joining or leaving
// (POST /api/me/cohorts, DELETE /api/me/cohorts/{cohort}) refreshes the flags at
// once, so an experiment's UI appears or goes without waiting for the next
// refresh. Experiments are unstable, and the page says so.
import { useEffect, useState } from "react";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { lookupMessage, useI18n } from "../../lib/i18n";
import { useFlagsClient } from "../../lib/flags";

export interface CohortItem {
  name: string;
  user_opt_in: boolean;
  description: string;
  joined: boolean;
}

export function ExperimentsSection() {
  const { t, locale } = useI18n();
  const toast = useToast();
  const flags = useFlagsClient();
  const [items, setItems] = useState<CohortItem[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<{ items?: CohortItem[] }>("/api/me/cohorts")
      .then((r) => setItems(Array.isArray(r?.items) ? r.items : []))
      .catch(() => setFailed(true));
  }, []);

  async function toggle(c: CohortItem) {
    if (busy) return;
    setBusy(c.name);
    try {
      if (c.joined) {
        await api.delete(`/api/me/cohorts/${encodeURIComponent(c.name)}`);
      } else {
        await api.post("/api/me/cohorts", { cohort: c.name });
      }
      setItems((list) =>
        list ? list.map((it) => (it.name === c.name ? { ...it, joined: !c.joined } : it)) : list
      );
      await flags?.refresh(0);
    } catch (e) {
      toast.show({ tone: "error", message: e instanceof ApiError ? e.message : t("common.error") });
    } finally {
      setBusy(null);
    }
  }

  return (
    <section className="mkt-settings__experiments" aria-labelledby="mkt-experiments-title">
      <h2 id="mkt-experiments-title">{t("settings.experiments.title")}</h2>
      <p className="mkt-alert" role="note">
        {t("settings.experiments.unstable")}
      </p>
      {failed ? (
        <p className="mkt-error" role="alert">
          {t("common.error")}
        </p>
      ) : items === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : items.length === 0 ? (
        <p className="mkt-muted">{t("settings.experiments.none")}</p>
      ) : (
        <ul className="mkt-settings__experiment-list">
          {items.map((c) => (
            <li key={c.name}>
              <Toggle
                checked={c.joined}
                disabled={busy !== null}
                onChange={() => void toggle(c)}
                label={lookupMessage(locale, `settings.experiments.cohort.${c.name}`) ?? c.name}
              />
              <p className="mkt-muted" dir="auto">
                {lookupMessage(locale, `settings.experiments.cohort.${c.name}.hint`) ??
                  c.description}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
