// What an old home kept aside (Epic 37, "Emergency promotion"): it wrote these after
// another machine took over, and reported them when it came back. The admin downloads
// them, or reviews what would apply (each of the old home's transactions whole or not,
// with why not) and applies it in one go; or sets the report aside.
//
//   GET  /api/cluster/kept-aside/{id}/download
//   GET  /api/cluster/kept-aside/{id}/plan
//   POST /api/cluster/kept-aside/{id}/apply    {plan}: 409 when the plan changed since
//   POST /api/cluster/kept-aside/{id}/dismiss
import { useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError, downloadBlob, type Problem } from "../../lib/api";
import { clusterApi, type KeptPlan, type KeptReport } from "../../lib/clusterApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useFmt } from "../queue/format";

export function KeptAside({ reports, onDone }: { reports: KeptReport[]; onDone: () => void }) {
  const { t } = useI18n();
  if (reports.length === 0) return null;
  return (
    <div className="mkt-cl-switch__kept">
      <h3 className="mkt-mc-section__subtitle">{t("machines.kept.title")}</h3>
      <ul className="mkt-cl-switch__reports">
        {reports.map((r) => (
          <Report key={r.id} r={r} onDone={onDone} />
        ))}
      </ul>
    </div>
  );
}

function Report({ r, onDone }: { r: KeptReport; onDone: () => void }) {
  const { t } = useI18n();
  const f = useFmt();
  const toast = useToast();
  const [plan, setPlan] = useState<KeptPlan | null>(null);
  const [changed, setChanged] = useState(false);
  const [busy, setBusy] = useState(false);
  const name = bidiIsolate(r.from_name);

  const review = () => {
    setBusy(true);
    setChanged(false);
    clusterApi
      .keptPlan(r.id)
      .then(setPlan)
      .catch(() => toast.show({ id: "kept", tone: "error", message: t("common.error") }))
      .finally(() => setBusy(false));
  };
  const apply = (p: KeptPlan) => {
    setBusy(true);
    clusterApi
      .keptApply(r.id, p.hash)
      .then((res) => {
        setPlan(null);
        toast.show({
          id: "kept",
          tone: "success",
          message: t("machines.kept.plan.done", { n: res.plan.apply }),
        });
        onDone();
      })
      .catch((e) => {
        // the plan changed since it was shown: nothing was applied, here is the new one
        const next =
          e instanceof ApiError ? (e.problem as Problem & { plan?: KeptPlan }).plan : undefined;
        if (e instanceof ApiError && e.status === 409 && next) {
          setPlan(next);
          setChanged(true);
          return;
        }
        toast.show({ id: "kept", tone: "error", message: t("common.error") });
      })
      .finally(() => setBusy(false));
  };
  const dismiss = () => {
    setBusy(true);
    clusterApi
      .keptDismiss(r.id)
      .then(() => {
        setPlan(null);
        onDone();
      })
      .catch(() => toast.show({ id: "kept", tone: "error", message: t("common.error") }))
      .finally(() => setBusy(false));
  };

  return (
    <li className="mkt-cl-switch__report">
      <p>{t("machines.kept.intro", { name })}</p>
      <p className="mkt-mc-muted">
        {r.lost
          ? t("machines.kept.lost", { name })
          : t("machines.kept.summary", {
              changes: r.changes,
              txs: r.transactions,
              ago: f.ago(r.received_at),
            })}
      </p>
      {r.state === "applied" && r.applied_at && (
        <p>
          <Badge tone="success">{t("machines.kept.state.applied")}</Badge>{" "}
          {t("machines.kept.applied", {
            n: r.applied_transactions ?? 0,
            txs: r.transactions,
            ago: f.ago(r.applied_at),
          })}
        </p>
      )}
      {r.state === "dismissed" && r.applied_at && (
        <p className="mkt-mc-muted">{t("machines.kept.dismissed", { ago: f.ago(r.applied_at) })}</p>
      )}
      <div className="mkt-mc-row">
        <Button
          variant="ghost"
          onClick={() =>
            void downloadBlob(
              clusterApi.keptDownloadUrl(r.id),
              `kept-aside-term-${r.term}.json`
            ).catch(() => toast.show({ id: "kept", tone: "error", message: t("common.error") }))
          }
        >
          {t("machines.kept.download")}
        </Button>
        {r.state === "reported" && !r.lost && (
          <Button variant="secondary" disabled={busy} onClick={review}>
            {t("machines.kept.review")}
          </Button>
        )}
        {r.state === "reported" && (
          <Button variant="ghost" disabled={busy} onClick={dismiss}>
            {t("machines.kept.dismiss")}
          </Button>
        )}
      </div>
      {plan && (
        <Modal
          open
          onClose={() => setPlan(null)}
          title={t("machines.kept.plan.title", { name })}
          footer={
            <>
              <Button variant="secondary" onClick={() => setPlan(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="primary"
                disabled={busy || plan.apply === 0}
                onClick={() => apply(plan)}
              >
                {t("machines.kept.plan.apply", { n: plan.apply })}
              </Button>
            </>
          }
        >
          <p>{t("machines.kept.plan.body")}</p>
          {changed && (
            <p className="mkt-mc-warn" role="alert">
              {t("machines.kept.plan.changed")}
            </p>
          )}
          {plan.unknown && <p className="mkt-mc-warn">{t("machines.kept.plan.unknown")}</p>}
          {!plan.unknown && plan.apply === 0 && (
            <p className="mkt-mc-muted">{t("machines.kept.plan.nothing")}</p>
          )}
          <table className="mkt-cl-switch__plan">
            <thead>
              <tr>
                <th scope="col">{t("machines.kept.plan.when")}</th>
                <th scope="col">{t("machines.kept.plan.tables")}</th>
                <th scope="col">{t("machines.kept.plan.result")}</th>
              </tr>
            </thead>
            <tbody>
              {(plan.transactions ?? []).map((tx) => (
                <tr key={tx.commit_lsn} data-apply={tx.apply}>
                  <td>{f.ago(tx.committed_at)}</td>
                  <td>
                    {tx.tables.map((tb) => bidiIsolate(tb.replace(/^public\./, ""))).join(", ")}
                    <span className="mkt-mc-muted"> ({tx.changes})</span>
                  </td>
                  <td>
                    {tx.apply ? (
                      <Badge tone="success">{t("machines.kept.applies")}</Badge>
                    ) : (
                      <span className="mkt-mc-muted">
                        {t(`machines.kept.reason.${tx.reason ?? "fails"}`, {
                          detail: tx.detail ?? "",
                        })}
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Modal>
      )}
    </li>
  );
}
