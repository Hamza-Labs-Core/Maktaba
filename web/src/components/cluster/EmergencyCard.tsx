// A standby home taking over as the home (Epic 37, "Emergency promotion"): shown on a
// standby's own Machines page. With its home gone for good, its admin makes it the
// home here, after a warning and the home's name typed to confirm. Offered only when it
// holds the home's keys, has a copy of the library and the home does not answer (two
// homes never write).
//
//   GET  /api/cluster/emergency   every 15 s (404 on a home: nothing shown)
//   POST /api/cluster/emergency   {confirm}
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { clusterApi, type EmergencyState } from "../../lib/clusterApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useFmt } from "../queue/format";

export const EMERGENCY_POLL_MS = 15_000;

export function EmergencyCard() {
  const [state, setState] = useState<EmergencyState | null>(null);
  useEffect(() => {
    let alive = true;
    const look = () =>
      clusterApi
        .emergency()
        .then((s) => {
          if (alive) setState(s);
        })
        .catch(() => {
          // a home (no such route), or this machine restarting: nothing to offer
          if (alive) setState(null);
        });
    void look();
    const id = setInterval(() => void look(), EMERGENCY_POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);
  if (!state?.standby) return null;
  return <EmergencyView state={state} />;
}

export function EmergencyView({ state }: { state: EmergencyState }) {
  const { t } = useI18n();
  const f = useFmt();
  const toast = useToast();
  const [asking, setAsking] = useState(false);
  const [typed, setTyped] = useState("");
  const [started, setStarted] = useState(false);
  const home = bidiIsolate(state.home);
  const go = () => {
    clusterApi
      .startEmergency(typed)
      .then(() => {
        setAsking(false);
        setStarted(true);
      })
      .catch(() => toast.show({ id: "emergency", tone: "error", message: t("emergency.refused") }));
  };
  const reasons = (state.reasons ?? []).filter((r) => r !== "no_kit");
  return (
    <section className="mkt-mc-section mkt-cl-emergency" aria-labelledby="mkt-cl-emergency-h">
      <h2 id="mkt-cl-emergency-h" className="mkt-mc-section__title">
        {t("emergency.title")}
      </h2>
      <p>{t("emergency.intro", { home })}</p>
      {state.kit_made_at && (
        <p className="mkt-mc-muted">
          {t("emergency.kit", { home, ago: f.ago(state.kit_made_at) })}
        </p>
      )}
      {state.copy?.applied_at && (
        <p className="mkt-mc-muted">{t("emergency.copy", { ago: f.ago(state.copy.applied_at) })}</p>
      )}
      {started ? (
        <p role="status">{t("emergency.started")}</p>
      ) : (
        <>
          {reasons.length > 0 && (
            <ul className="mkt-mc-muted">
              {reasons.map((r) => (
                <li key={r}>{t(`emergency.reason.${r}`, { home })}</li>
              ))}
            </ul>
          )}
          <Button variant="destructive" disabled={!state.ready} onClick={() => setAsking(true)}>
            {t("emergency.go")}
          </Button>
        </>
      )}
      {asking && (
        <Modal
          open
          dismissable={false}
          onClose={() => setAsking(false)}
          title={t("emergency.confirm.title")}
          footer={
            <>
              <Button variant="secondary" onClick={() => setAsking(false)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                disabled={typed.trim().toLowerCase() !== state.home.trim().toLowerCase()}
                onClick={go}
              >
                {t("emergency.confirm.go")}
              </Button>
            </>
          }
        >
          <p>{t("emergency.confirm.body", { home })}</p>
          <Input
            label={t("emergency.confirm.label", { home })}
            value={typed}
            autoComplete="off"
            onChange={(e) => setTyped(e.currentTarget.value)}
          />
        </Modal>
      )}
    </section>
  );
}
