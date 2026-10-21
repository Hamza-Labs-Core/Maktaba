// Switching the home on the Machines tab (Epic 37, specs/epics/37-node-replica):
// the cluster's term and its home, the machines that could take over (and why
// one cannot now), a switch under way step by step, and the last one's result:
// each machine following, each outside service moved, and a checklist of what
// needs a person on the new home. Starting a switch asks first: changes are
// refused for a minute or two while the home hands over. Below: the standby
// homes (StandbyHomes), and what an old home kept aside (KeptAside).
//
//   GET  /api/cluster/switchover                  every 5 s while the tab is shown
//   POST /api/cluster/switchover                  {to_node}
//   POST /api/cluster/switchover/{id}/checklist   {item, done}
import { memo, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import {
  clusterApi,
  type SwitchCandidate,
  type SwitchIntegration,
  type SwitchProgress,
  type SwitchStatus,
} from "../../lib/clusterApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useFmt } from "../queue/format";
import { KeptAside } from "./KeptAside";
import { StandbyHomes } from "./StandbyHomes";

export const SWITCH_POLL_MS = 5_000;

/** The steps of a planned switch, in order (the old home's). */
export const SWITCH_STEPS = [
  "check",
  "drain",
  "freeze",
  "marker",
  "kit",
  "promote",
  "repoint",
  "demote",
] as const;

const ENDED = new Set(["done", "failed", "cancelled", "handed_over"]);

const STATE_TONE: Record<string, "success" | "neutral" | "warn" | "error"> = {
  done: "success",
  handed_over: "success",
  failed: "error",
  cancelled: "neutral",
};

const INTEGRATION_TONE = {
  moved: "success",
  pending: "neutral",
  failed: "error",
  needs_you: "warn",
  not_used: "neutral",
} as const;

export function SwitchHomeCard() {
  const [status, setStatus] = useState<SwitchStatus | null>(null);
  const [error, setError] = useState(false);
  const last = useRef("");
  const reload = useRef<() => void>(() => {});

  useEffect(() => {
    let alive = true;
    const load = () => {
      clusterApi
        .switchover()
        .then((s) => {
          if (!alive) return;
          setError(false);
          const key = JSON.stringify(s);
          if (key === last.current) return;
          last.current = key;
          setStatus(s);
        })
        .catch(() => {
          if (alive) setError(true);
        });
    };
    reload.current = load;
    load();
    const id = setInterval(load, SWITCH_POLL_MS);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  return <SwitchHomeView status={status} error={error} onRefresh={() => reload.current()} />;
}

export const SwitchHomeView = memo(function SwitchHomeView({
  status,
  error,
  onRefresh,
}: {
  status: SwitchStatus | null;
  error: boolean;
  onRefresh?: () => void;
}) {
  const { t } = useI18n();
  const f = useFmt();
  const toast = useToast();
  const [asking, setAsking] = useState<SwitchCandidate | null>(null);
  const [busy, setBusy] = useState(false);
  const term = status?.term ?? null;
  const current = status?.current && !ENDED.has(status.current.state) ? status.current : null;
  const lastOne = (status?.recent ?? []).find((r) => ENDED.has(r.state)) ?? null;
  const candidates = status?.candidates ?? [];

  const start = (c: SwitchCandidate) => {
    setAsking(null);
    setBusy(true);
    clusterApi
      .startSwitch(c.node_id)
      .then(() =>
        toast.show({
          id: "switch",
          tone: "info",
          message: t("machines.switch.started", { name: c.name }),
        })
      )
      .catch(() =>
        toast.show({ id: "switch", tone: "error", message: t("machines.switch.refused") })
      )
      .finally(() => setBusy(false));
  };

  return (
    <section className="mkt-mc-section mkt-cl-switch" aria-labelledby="mkt-cl-switch-h">
      <div className="mkt-mc-row">
        <h2 id="mkt-cl-switch-h" className="mkt-mc-section__title">
          {t("machines.switch.title")}
        </h2>
        {term && (
          <Badge tone="neutral" role="status">
            {t("machines.switch.term", { n: term.term })}
          </Badge>
        )}
      </div>
      {/* nothing until it answers: the page shows its own loading state */}
      {(term || error) && (
        <p className="mkt-mc-muted">
          {term
            ? t(`machines.switch.home.${term.reason}`, {
                name: bidiIsolate(term.home_name),
                since: f.ago(term.started_at),
              })
            : t("machines.switch.unavailable")}
        </p>
      )}

      {current && <Progress p={current} />}

      {!current && candidates.length > 0 && (
        <ul className="mkt-cl-switch__candidates">
          {candidates.map((c) => (
            <li key={c.node_id} className="mkt-mc-row">
              <span>{bidiIsolate(c.name)}</span>
              <Button variant="secondary" disabled={!c.ready || busy} onClick={() => setAsking(c)}>
                {t("machines.switch.make", { name: bidiIsolate(c.name) })}
              </Button>
              {!c.ready && (c.reasons ?? []).length > 0 && (
                <span className="mkt-mc-muted">
                  {(c.reasons ?? []).map((r) => t(`machines.switch.reason.${r}`)).join(" · ")}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      {!current && status && candidates.length === 0 && (
        <p className="mkt-mc-muted">{t("machines.switch.none")}</p>
      )}

      {!current && lastOne && <Result p={lastOne} onRefresh={onRefresh} />}

      {status && <StandbyHomes standbys={status.standbys ?? []} onChange={() => onRefresh?.()} />}
      {status && <KeptAside reports={status.reports ?? []} onDone={() => onRefresh?.()} />}

      {asking && (
        <Modal
          open
          dismissable={false}
          onClose={() => setAsking(null)}
          title={t("machines.switch.confirm.title", { name: bidiIsolate(asking.name) })}
          footer={
            <>
              <Button variant="secondary" onClick={() => setAsking(null)}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" onClick={() => start(asking)}>
                {t("machines.switch.confirm.go")}
              </Button>
            </>
          }
        >
          <p>
            {t("machines.switch.confirm.body", {
              name: bidiIsolate(asking.name),
              home: bidiIsolate(term?.home_name ?? ""),
            })}
          </p>
          <p className="mkt-mc-muted">{t("machines.switch.confirm.after")}</p>
        </Modal>
      )}
    </section>
  );
});

function Progress({ p }: { p: SwitchProgress }) {
  const { t } = useI18n();
  const steps = new Map((p.steps ?? []).map((s) => [s.name, s]));
  return (
    <div className="mkt-cl-switch__progress" role="status">
      <p>
        {t("machines.switch.running", { name: bidiIsolate(p.to_name) })}{" "}
        <Badge tone={STATE_TONE[p.state] ?? "neutral"}>
          {t(`machines.switch.state.${p.state}`)}
        </Badge>
      </p>
      {steps.size > 0 && (
        <ol className="mkt-cl-switch__steps">
          {SWITCH_STEPS.map((name) => {
            const s = steps.get(name);
            return (
              <li key={name} data-state={s?.state ?? "waiting"}>
                {t(`machines.switch.step.${name}`)}
                {s && s.state !== "done" && (
                  <span className="mkt-mc-muted">
                    {" "}
                    — {t(`machines.switch.stepState.${s.state}`)}
                  </span>
                )}
              </li>
            );
          })}
        </ol>
      )}
      <Machines p={p} />
      <Integrations p={p} />
    </div>
  );
}

function Machines({ p }: { p: SwitchProgress }) {
  const { t } = useI18n();
  const ms = Object.entries(p.machines ?? {}).filter(([id]) => id !== p.to_node);
  if (ms.length === 0) return null;
  return (
    <ul className="mkt-cl-switch__machines">
      {ms.map(([id, m]) => (
        <li key={id}>
          {bidiIsolate(m.name)}: {t(`machines.switch.machine.${m.state}`)}
        </li>
      ))}
    </ul>
  );
}

/** The outside services the switch moved, is moving, or needs a person for (after it,
 * a checklist on the new home: done once they say so, or once a check sees it). */
function Integrations({
  p,
  checklist,
  onRefresh,
}: {
  p: SwitchProgress;
  checklist?: boolean;
  onRefresh?: () => void;
}) {
  const { t } = useI18n();
  const items = Object.entries(p.integrations ?? {}).filter(([, v]) => v.state !== "not_used");
  const needs = items.filter(([, v]) => v.state === "needs_you" || v.state === "failed");
  if (items.length === 0) return null;
  if (checklist && needs.length > 0) {
    return (
      <>
        <IntegrationList items={items} />
        <Checklist p={p} needs={needs} onRefresh={onRefresh} />
      </>
    );
  }
  return (
    <>
      <ul className="mkt-cl-switch__integrations">
        {items.map(([name, v]) => (
          <li key={name}>
            <Badge tone={INTEGRATION_TONE[v.state] ?? "neutral"}>
              {t(`machines.switch.integration.state.${v.state}`)}
            </Badge>{" "}
            {t(`machines.switch.integration.${name}`)}
          </li>
        ))}
      </ul>
      {needs.length > 0 && (
        <ul className="mkt-cl-switch__needs" role="note">
          {needs.map(([name, v]) => (
            <li key={name}>{t(`machines.switch.needs.${name}`, { detail: v.detail ?? "" })}</li>
          ))}
        </ul>
      )}
    </>
  );
}

function IntegrationList({ items }: { items: [string, SwitchIntegration][] }) {
  const { t } = useI18n();
  return (
    <ul className="mkt-cl-switch__integrations">
      {items.map(([name, v]) => (
        <li key={name}>
          <Badge tone={INTEGRATION_TONE[v.state] ?? "neutral"}>
            {t(`machines.switch.integration.state.${v.state}`)}
          </Badge>{" "}
          {t(`machines.switch.integration.${name}`)}
        </li>
      ))}
    </ul>
  );
}

/** What a person does on the new home after a switch, ticked off as done. */
function Checklist({
  p,
  needs,
  onRefresh,
}: {
  p: SwitchProgress;
  needs: [string, SwitchIntegration][];
  onRefresh?: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const mark = (item: string, done: boolean) => {
    setBusy(true);
    clusterApi
      .checklistDone(p.id, item, done)
      .then(() => onRefresh?.())
      .catch(() => toast.show({ id: "checklist", tone: "error", message: t("common.error") }))
      .finally(() => setBusy(false));
  };
  return (
    <div className="mkt-cl-switch__checklist" role="note">
      <p>{t("machines.checklist.title", { name: bidiIsolate(p.to_name) })}</p>
      <ul>
        {needs.map(([name, v]) => (
          <li key={name} data-done={v.done ? "true" : "false"}>
            {v.done && <Badge tone="success">{t("machines.checklist.checked")}</Badge>}{" "}
            <span>{t(`machines.switch.needs.${name}`, { detail: v.detail ?? "" })}</span>{" "}
            <Button variant="ghost" size="sm" disabled={busy} onClick={() => mark(name, !v.done)}>
              {v.done ? t("machines.checklist.undo") : t("machines.checklist.done")}
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Result({ p, onRefresh }: { p: SwitchProgress; onRefresh?: () => void }) {
  const { t } = useI18n();
  return (
    <div className="mkt-cl-switch__result">
      <p>
        <Badge tone={STATE_TONE[p.state] ?? "neutral"}>
          {t(`machines.switch.state.${p.state}`)}
        </Badge>{" "}
        {t("machines.switch.last", { from: bidiIsolate(p.from_name), to: bidiIsolate(p.to_name) })}
      </p>
      {p.state === "failed" && p.detail && <p className="mkt-mc-warn">{p.detail}</p>}
      <Machines p={p} />
      <Integrations p={p} checklist={p.state === "done"} onRefresh={onRefresh} />
    </div>
  );
}
