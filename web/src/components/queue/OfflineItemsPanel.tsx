// Epic 47: where each item of the offline pipelines is. Per item the order is
// download -> convert -> dedup check; a row is the item's latest job in that
// step while it waits, runs or failed, with the job's own Cancel and Retry.
import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { OFFLINE_STEPS, queueApi } from "../../lib/queueApi";
import type { OfflineItem, OfflineStep } from "../../lib/queueApi";
import { useFmt } from "./format";
import { useAction } from "./useAction";

const LIVE = new Set(["pending", "claimed", "running", "resuming", "paused"]);
/** the steps a person follows an item through (restore and purge are on File actions) */
const SHOWN: OfflineStep[] = OFFLINE_STEPS.slice(0, 3);

export interface OfflineItemsPanelProps {
  /** bumps when jobs finish, so the list follows the work */
  tick?: number;
  libraryId?: string;
}

export function OfflineItemsPanel({ tick, libraryId }: OfflineItemsPanelProps) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const { busy, error, run } = useAction();
  const [step, setStep] = useState<OfflineStep | "">("");
  const [items, setItems] = useState<OfflineItem[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [loadError, setLoadError] = useState(false);

  const load = useCallback(() => {
    queueApi
      .offlineItems({ library_id: libraryId, step: step || undefined, limit: 50 })
      .then((r) => {
        setItems(
          Array.isArray(r?.items) ? r.items.filter((i) => step || SHOWN.includes(i.step)) : []
        );
        setNext(r?.next ?? null);
        setLoadError(false);
      })
      .catch(() => setLoadError(true));
  }, [libraryId, step]);

  useEffect(() => {
    load();
  }, [load, tick]);

  const more = () => {
    if (!next) return;
    queueApi
      .offlineItems({ library_id: libraryId, step: step || undefined, before: next, limit: 50 })
      .then((r) => {
        setItems((prev) => [...(prev ?? []), ...(r?.items ?? [])]);
        setNext(r?.next ?? null);
      })
      .catch(() => setLoadError(true));
  };

  const control = (it: OfflineItem, what: "cancel" | "retry") =>
    void run(`${what}:${it.id}`, () =>
      what === "cancel" ? queueApi.cancelJob(it.id) : queueApi.retryJob(it.id)
    ).then((r) => r !== undefined && load());

  const stateText = (it: OfflineItem) => {
    if (it.waiting_for_conversion) return t("queue.items.waitingConversion");
    if (it.state === "failed") return t("queue.items.state.failed");
    if (LIVE.has(it.state)) {
      return it.state === "pending"
        ? t("queue.items.state.queued")
        : t(`queue.items.doing.${it.step}`);
    }
    return t(`queue.items.state.${it.state}`);
  };

  return (
    <section className="mkt-q-panel mkt-q-items" aria-labelledby="mkt-q-items-h">
      <div className="mkt-q-section-head">
        <h2 className="mkt-q-section-title" id="mkt-q-items-h">
          {t("queue.items.title")}
        </h2>
        <label className="mkt-q-field mkt-q-field--inline">
          {t("queue.items.step")}
          <select value={step} onChange={(e) => setStep(e.target.value as OfflineStep | "")}>
            <option value="">{t("queue.items.allSteps")}</option>
            {SHOWN.map((s) => (
              <option key={s} value={s}>
                {t(`queue.items.steps.${s}`)}
              </option>
            ))}
          </select>
        </label>
      </div>
      <p className="mkt-q-muted">{t("queue.items.intro")}</p>
      {error && (
        <p role="alert" className="mkt-q-error">
          {error}
        </p>
      )}
      {/* a list that did not load is not an alert: the page's own banner says so */}
      {loadError && <p className="mkt-q-error">{t("queue.items.loadError")}</p>}
      {items === null && !loadError && <p className="mkt-loading">{t("common.loading")}</p>}
      {items && items.length === 0 && <p className="mkt-q-empty">{t("queue.items.none")}</p>}
      {items && items.length > 0 && (
        <div className="mkt-q-tablewrap">
          <table className="mkt-q-table">
            <thead>
              <tr>
                <th scope="col">{t("queue.items.col.item")}</th>
                <th scope="col">{t("queue.items.col.step")}</th>
                <th scope="col">{t("queue.items.col.state")}</th>
                <th scope="col">{t("queue.items.col.since")}</th>
                <th scope="col">
                  <span className="mkt-visually-hidden">{t("queue.libs.col.actions")}</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((it) => (
                <tr key={it.id} data-state={it.state}>
                  <th scope="row">
                    <Link
                      to={
                        (it.subject_kind as string) === "dup_group"
                          ? "/queue/duplicates?status=all"
                          : `/queue/items/${encodeURIComponent(it.subject_kind)}/${encodeURIComponent(it.subject_id)}`
                      }
                    >
                      {bidiIsolate(it.subject_label)}
                    </Link>
                    {it.downloading && (
                      <div className="mkt-q-muted" dir="ltr">
                        {t("queue.items.fetching", { id: it.downloading })}
                      </div>
                    )}
                  </th>
                  <td>
                    <span className="mkt-q-chip">{t(`queue.items.steps.${it.step}`)}</span>
                  </td>
                  <td>
                    <strong>{stateText(it)}</strong>
                    {it.progress && it.progress.total_units > 0 && LIVE.has(it.state) && (
                      <div className="mkt-q-muted">
                        {t("queue.items.progress", {
                          done: n(it.progress.done_units),
                          total: n(it.progress.total_units),
                        })}
                      </div>
                    )}
                    {it.error && (
                      <div className="mkt-q-error" dir="auto">
                        {it.error}
                      </div>
                    )}
                  </td>
                  <td>{fmt.ago(it.finished_at ?? it.started_at ?? it.created_at)}</td>
                  <td>
                    {LIVE.has(it.state) && !it.waiting_for_conversion && (
                      <Button
                        size="sm"
                        variant="ghost"
                        loading={busy === `cancel:${it.id}`}
                        disabled={busy != null && busy !== `cancel:${it.id}`}
                        onClick={() => control(it, "cancel")}
                      >
                        {t("queue.items.cancel")}
                        <span className="mkt-visually-hidden"> {it.subject_label}</span>
                      </Button>
                    )}
                    {it.state === "failed" && (
                      <Button
                        size="sm"
                        variant="secondary"
                        loading={busy === `retry:${it.id}`}
                        disabled={busy != null && busy !== `retry:${it.id}`}
                        onClick={() => control(it, "retry")}
                      >
                        {t("queue.items.retry")}
                        <span className="mkt-visually-hidden"> {it.subject_label}</span>
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {next && (
            <Button size="sm" variant="secondary" onClick={more}>
              {t("queue.failures.more")}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
