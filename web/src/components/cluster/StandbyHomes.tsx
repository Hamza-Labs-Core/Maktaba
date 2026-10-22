// The standby homes on the Machines tab (Epic 37, "Emergency promotion"): a full
// machine the admin marks may take over as the home if the home is gone for good.
// The home keeps its secrets and keys sealed to each one, current; the mark removed,
// the machine's copy of them is removed.
//
//   PUT /api/cluster/switchover/standby  {node_id, standby}
import { useState } from "react";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { clusterApi, type StandbyHome } from "../../lib/clusterApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useFmt } from "../queue/format";

export function StandbyHomes({
  standbys,
  onChange,
}: {
  standbys: StandbyHome[];
  onChange: (next: StandbyHome[]) => void;
}) {
  const { t } = useI18n();
  const f = useFmt();
  const toast = useToast();
  const [busy, setBusy] = useState<string | null>(null);
  const set = (s: StandbyHome, on: boolean) => {
    setBusy(s.node_id);
    clusterApi
      .setStandby(s.node_id, on)
      .then((r) => onChange(r.standbys ?? []))
      .catch(() =>
        toast.show({ id: "standby", tone: "error", message: t("machines.standby.failed") })
      )
      .finally(() => setBusy(null));
  };
  const any = standbys.some((s) => s.can || s.standby);
  return (
    <div className="mkt-cl-switch__standby">
      <h3 className="mkt-mc-section__subtitle">{t("machines.standby.title")}</h3>
      <p className="mkt-mc-muted">{t("machines.standby.intro")}</p>
      {!any && <p className="mkt-mc-muted">{t("machines.standby.none")}</p>}
      {any && (
        <ul className="mkt-cl-switch__candidates">
          {standbys
            .filter((s) => s.can || s.standby)
            .map((s) => (
              <li key={s.node_id} className="mkt-mc-row">
                <Toggle
                  label={bidiIsolate(s.name)}
                  checked={s.standby}
                  disabled={busy !== null || (!s.can && !s.standby)}
                  onChange={(e) => set(s, e.currentTarget.checked)}
                />
                <span className="mkt-mc-muted">
                  {s.standby
                    ? s.kit_at
                      ? t("machines.standby.kit", { ago: f.ago(s.kit_at) })
                      : t("machines.standby.waiting")
                    : !s.can
                      ? t("machines.standby.cannot")
                      : ""}
                </span>
              </li>
            ))}
        </ul>
      )}
    </div>
  );
}
