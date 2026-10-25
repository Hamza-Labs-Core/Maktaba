// The control centre's update card (story 35.15 AC3): while an update runs, its release,
// where it is and how many machines are done, with a link to the Updates page. Nothing
// shows when no run is active, or for someone the updates API refuses (not an admin).
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { lookupMessage, useI18n } from "../../../lib/i18n";
import { updatesApi, type UpdateRun, type UpdateTarget } from "../../../lib/updatesApi";

/** How often the card asks while it is shown. */
export const UPDATE_CARD_POLL_MS = 15_000;

/** The target states whose part in the run is over (api/internal/updates targetEnded). */
const TARGET_ENDED = new Set([
  "healthy",
  "rolled_back",
  "failed",
  "failed_prepare",
  "skipped",
  "worker_only",
  "held",
  "offline",
  "needs_person",
]);

export function ControlCentreUpdateCard() {
  const { t, n, locale } = useI18n();
  const [run, setRun] = useState<UpdateRun | null>(null);
  const [targets, setTargets] = useState<UpdateTarget[]>([]);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const o = await updatesApi.overview();
        if (!alive) return;
        if (!o?.run) {
          setRun(null);
          setTargets([]);
          return;
        }
        setRun(o.run);
        const v = await updatesApi.run(o.run.id);
        if (alive) setTargets(Array.isArray(v?.targets) ? v.targets : []);
      } catch {
        if (alive) setRun(null); // not an admin, or the API is away: no card
      }
    };
    void load();
    const id = setInterval(() => void load(), UPDATE_CARD_POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  if (!run) return null;
  const word = (group: string, value: string) => {
    const key = `updates.${group}.${value}`;
    return lookupMessage(locale, key) !== undefined ? t(key) : value.replace(/_/g, " ");
  };
  const done = targets.filter((x) => TARGET_ENDED.has(x.state)).length;
  return (
    <section className="mkt-q-panel mkt-update-card" aria-labelledby="mkt-update-card-h">
      <h2 className="mkt-q-section-title" id="mkt-update-card-h">
        {t("updates.card.title", { version: run.version })}
      </h2>
      <p>
        {word("state", run.state)}
        {run.step ? ` · ${word("step", run.step)}` : ""}
      </p>
      {targets.length > 0 && (
        <p>{t("updates.card.machines", { done: n(done), total: n(targets.length) })}</p>
      )}
      <Link to="/admin/updates">{t("updates.card.open")}</Link>
    </section>
  );
}
