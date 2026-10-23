// Admin -> Updates (Epic 35, story 35.13; install-kinds.md): the cluster's automatic
// updates on one page. Updates install by themselves, so the page is mostly for reading:
// every machine's version and install kind, the next planned update (or why there is
// none), the run under way step by step (the home's steps, then one row per machine),
// and what happened before. The few actions are here too: Update now, Check now, pause
// (automatic installs off), Cancel a run before the home switches, Retry, Skip or Roll
// back a machine, Resume a held rollout.
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  RUN_CANCELLABLE,
  RUN_ENDED,
  updatesApi,
  type UpdateMachine,
  type UpdateRun,
  type UpdateRunView,
  type UpdateTarget,
  type UpdatesOverview,
} from "../../lib/updatesApi";

type Tone = "neutral" | "accent" | "success" | "warn" | "error";

/** A state's badge tone. */
export function stateTone(state: string): Tone {
  switch (state) {
    case "up_to_date":
    case "healthy":
    case "done":
    case "home_committed":
    case "committed":
      return "success";
    case "failed":
    case "failed_prepare":
    case "failed_rollback":
    case "rolled_back":
      return "error";
    case "held":
    case "behind":
    case "offline":
    case "worker_only":
    case "ahead_of_home":
    case "done_with_failures":
    case "cancelled":
      return "warn";
    default:
      return "accent";
  }
}

/** The page's words for a state, falling back to the raw state for one it has none for. */
function useStateLabel() {
  const { t } = useI18n();
  return (state: string) => {
    const key = `updates.state.${state}`;
    const s = t(key);
    return s === key ? state.replace(/_/g, " ") : s;
  };
}

function when(iso?: string): string {
  return iso ? new Date(iso).toLocaleString() : "";
}

function duration(run: UpdateRun): string {
  if (!run.finished_at) return "";
  const s = Math.max(0, (Date.parse(run.finished_at) - Date.parse(run.started_at)) / 1000);
  return s < 120 ? `${Math.round(s)} s` : `${Math.round(s / 60)} min`;
}

export function AdminUpdates() {
  const { t } = useI18n();
  const toast = useToast();
  const label = useStateLabel();
  const [ov, setOv] = useState<UpdatesOverview | null>(null);
  const [view, setView] = useState<UpdateRunView | null>(null);
  const [history, setHistory] = useState<UpdateRun[]>([]);
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [confirmNow, setConfirmNow] = useState(false);

  const load = useCallback(async () => {
    try {
      const o = await updatesApi.overview();
      setOv(o);
      setError(false);
      const shown = o.run ?? o.last_run;
      setView(shown ? await updatesApi.run(shown.id) : null);
      setHistory((await updatesApi.runs(20)).items ?? []);
    } catch {
      setError(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // a run under way is followed live (the home's API restarts during it: a failed read
  // is tried again at the next tick)
  const running = Boolean(ov?.run && !RUN_ENDED.has(ov.run.state));
  useEffect(() => {
    if (!running) return undefined;
    const id = setInterval(() => void load(), 5000);
    return () => clearInterval(id);
  }, [running, load]);

  const act = async (fn: () => Promise<unknown>, done: string) => {
    setBusy(true);
    try {
      await fn();
      toast.show({ tone: "success", message: done });
      await load();
    } catch (e) {
      toast.show({ tone: "error", message: e instanceof ApiError ? e.message : t("common.error") });
    } finally {
      setBusy(false);
    }
  };

  if (error && !ov) {
    return (
      <section className="mkt-page mkt-updates">
        <h1>{t("updates.page.title")}</h1>
        <p className="mkt-error" role="alert">
          {t("updates.page.unavailable")}
        </p>
      </section>
    );
  }
  if (!ov) {
    return (
      <section className="mkt-page mkt-updates" aria-busy="true">
        <h1>{t("updates.page.title")}</h1>
      </section>
    );
  }

  const target = ov.plan?.target;
  let next = t("updates.plan.none");
  if (target && ov.plan.held) next = t("updates.plan.held", { version: target.version });
  else if (target && ov.plan.next_run_at)
    next = t("updates.plan.at", { version: target.version, at: when(ov.plan.next_run_at) });
  else if (target) {
    const key = `updates.plan.waiting.${ov.plan.waiting ?? "auto_install_off"}`;
    next = t(key, { version: target.version });
    if (next === key)
      next = t("updates.plan.waiting.automatic_installs_not_built", { version: target.version });
  }
  const machines: UpdateMachine[] = ov.machines ?? [];
  const paused = !ov.policy.auto_install;

  return (
    <section className="mkt-page mkt-updates" aria-labelledby="updates-page-title">
      <h1 id="updates-page-title">{t("updates.page.title")}</h1>
      <p data-testid="updates-cluster-version">
        {t("updates.page.version", { version: ov.watermark || ov.current_version })}
      </p>
      <p className="mkt-muted" data-testid="updates-next">
        {next}
      </p>
      {paused && (
        <p className="mkt-muted" data-testid="updates-paused">
          {t("updates.page.paused")}
        </p>
      )}
      {ov.check_stale && (
        <p className="mkt-error" role="alert">
          {t("updates.settings.stale")}
        </p>
      )}
      <div className="mkt-updates__actions">
        <Button
          variant="primary"
          disabled={busy || running || !target}
          onClick={() => setConfirmNow(true)}
        >
          {t("updates.page.updateNow")}
        </Button>
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() => act(() => updatesApi.check(), t("updates.page.checked"))}
        >
          {t("updates.page.check")}
        </Button>
        {target && !running && (
          <Button
            variant="secondary"
            disabled={busy}
            onClick={() =>
              act(
                () => updatesApi.skip(target.version),
                t("updates.held.skipped", { version: target.version })
              )
            }
          >
            {t("updates.held.skip")}
          </Button>
        )}
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            act(
              () => updatesApi.saveSettings({ auto_install: paused }),
              paused ? t("updates.page.resumedAuto") : t("updates.page.pausedAuto")
            )
          }
        >
          {paused ? t("updates.page.resumeAuto") : t("updates.page.pauseAuto")}
        </Button>
      </div>

      <h2>{t("updates.page.machines")}</h2>
      <table className="mkt-table" data-testid="updates-machines">
        <thead>
          <tr>
            <th scope="col">{t("updates.page.col.machine")}</th>
            <th scope="col">{t("updates.page.col.version")}</th>
            <th scope="col">{t("updates.page.col.install")}</th>
            <th scope="col">{t("updates.page.col.state")}</th>
          </tr>
        </thead>
        <tbody>
          {machines.map((m) => (
            <tr key={m.id} data-testid={`updates-machine-${m.id}`}>
              <td>
                {m.role === "home" ? t("updates.page.home") : m.name}
                {m.platform ? <span className="mkt-muted"> · {m.platform}</span> : null}
              </td>
              <td dir="ltr">{m.version || "-"}</td>
              <td>{m.kind ? t(`updates.kind.${m.kind}`) : "-"}</td>
              <td>
                <Badge tone={stateTone(m.state)}>{label(m.state)}</Badge>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {view && (
        <RunPanel
          view={view}
          live={running}
          busy={busy}
          onCancel={() => act(() => updatesApi.cancel(view.id), t("updates.page.cancelled"))}
          onResume={() => act(() => updatesApi.resume(view.id), t("updates.page.resumed"))}
          onTarget={(node, action) =>
            act(
              () => updatesApi.targetAction(view.id, node, action),
              t(`updates.page.target.${action}Done`)
            )
          }
        />
      )}

      <h2>{t("updates.page.history")}</h2>
      {history.length === 0 ? (
        <p className="mkt-muted">{t("updates.page.noHistory")}</p>
      ) : (
        <table className="mkt-table" data-testid="updates-history">
          <thead>
            <tr>
              <th scope="col">{t("updates.page.col.version")}</th>
              <th scope="col">{t("updates.page.col.trigger")}</th>
              <th scope="col">{t("updates.page.col.state")}</th>
              <th scope="col">{t("updates.page.col.started")}</th>
              <th scope="col">{t("updates.page.col.took")}</th>
            </tr>
          </thead>
          <tbody>
            {history.map((r) => (
              <tr key={r.id}>
                <td dir="ltr">
                  {r.from_version ? `${r.from_version} → ` : ""}
                  {r.version}
                </td>
                <td>{t(`updates.trigger.${r.trigger}`)}</td>
                <td>
                  <Badge tone={stateTone(r.state)}>{label(r.state)}</Badge>
                </td>
                <td>{when(r.started_at)}</td>
                <td>{duration(r)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <Modal
        open={confirmNow}
        onClose={() => setConfirmNow(false)}
        title={t("updates.page.confirmTitle", { version: target?.version ?? "" })}
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmNow(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              variant="primary"
              disabled={busy}
              onClick={() => {
                setConfirmNow(false);
                if (target)
                  void act(
                    () => updatesApi.runNow(target.version),
                    t("updates.held.started", { version: target.version })
                  );
              }}
            >
              {t("updates.page.updateNow")}
            </Button>
          </>
        }
      >
        <p>{t("updates.page.confirmBody")}</p>
      </Modal>
    </section>
  );
}

interface RunPanelProps {
  view: UpdateRunView;
  live: boolean;
  busy: boolean;
  onCancel: () => void;
  onResume: () => void;
  onTarget: (node: string, action: "retry" | "skip" | "rollback") => void;
}

function RunPanel({ view, live, busy, onCancel, onResume, onTarget }: RunPanelProps) {
  const { t } = useI18n();
  const label = useStateLabel();
  const homeSteps = view.steps.filter((s) => s.host === "home");
  const ended = RUN_ENDED.has(view.state);
  const held = view.targets.some((x) => x.state === "held");
  return (
    <section aria-labelledby="updates-run-title" data-testid="updates-run">
      <h2 id="updates-run-title">
        {live
          ? t("updates.page.runLive", { version: view.version })
          : t("updates.page.runLast", { version: view.version })}
      </h2>
      <p>
        <Badge tone={stateTone(view.state)}>{label(view.state)}</Badge>{" "}
        {view.detail?.waiting && (
          <span className="mkt-muted">{t(`updates.waiting.${view.detail.waiting}`)}</span>
        )}
      </p>
      {view.error?.code && (
        <p
          className={ended && view.state !== "done" ? "mkt-error" : "mkt-muted"}
          data-testid="updates-run-error"
        >
          {t("updates.page.failedCheck", { code: String(view.error.code) })}
          {view.error.detail ? `: ${String(view.error.detail)}` : ""}
        </p>
      )}
      <div className="mkt-updates__actions">
        {!ended && RUN_CANCELLABLE.has(view.state) && (
          <Button variant="secondary" disabled={busy} onClick={onCancel}>
            {t("updates.page.cancel")}
          </Button>
        )}
        {ended && held && (
          <Button variant="secondary" disabled={busy} onClick={onResume}>
            {t("updates.page.resume")}
          </Button>
        )}
        <a href={updatesApi.logURL(view.id)} target="_blank" rel="noreferrer">
          {t("updates.page.log")}
        </a>
      </div>
      {!view.detail?.no_home && (
        <>
          <h3>
            {t("updates.page.homeSteps", {
              kind: view.install_kind ? t(`updates.kind.${view.install_kind}`) : "",
            })}
          </h3>
          <ol className="mkt-updates__steps" data-testid="updates-home-steps">
            {homeSteps.map((s) => (
              <li key={s.seq}>
                <Badge tone={stateTone(s.state === "done" ? "done" : s.state)}>
                  {t(`updates.step.${s.step}`)}
                </Badge>{" "}
                <span className="mkt-muted">
                  {label(s.state)}
                  {s.detail?.detail ? ` · ${s.detail.detail}` : ""}
                </span>
              </li>
            ))}
          </ol>
        </>
      )}
      {view.targets.length > 0 && (
        <>
          <h3>{t("updates.page.targets")}</h3>
          <table className="mkt-table" data-testid="updates-targets">
            <tbody>
              {view.targets.map((x) => (
                <TargetRow
                  key={x.node_id}
                  target={x}
                  ended={ended}
                  busy={busy}
                  onTarget={onTarget}
                />
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}

function TargetRow({
  target,
  ended,
  busy,
  onTarget,
}: {
  target: UpdateTarget;
  ended: boolean;
  busy: boolean;
  onTarget: (node: string, action: "retry" | "skip" | "rollback") => void;
}) {
  const { t } = useI18n();
  const label = useStateLabel();
  const failed = ["failed", "failed_prepare", "rolled_back", "held", "offline"].includes(
    target.state
  );
  return (
    <tr data-testid={`updates-target-${target.node_id}`}>
      <td>
        {target.name}
        {target.platform ? <span className="mkt-muted"> · {target.platform}</span> : null}
      </td>
      <td dir="ltr">
        {target.from_version} → {target.to_version}
      </td>
      <td>
        <Badge tone={stateTone(target.state)}>{label(target.state)}</Badge>
        {target.error?.held_by ? (
          <span className="mkt-muted">
            {" "}
            {t("updates.page.heldBy", { machine: String(target.error.held_by) })}
          </span>
        ) : target.error?.code ? (
          <span className="mkt-muted">
            {" "}
            {t("updates.page.failedCheck", { code: String(target.error.code) })}
          </span>
        ) : null}
      </td>
      <td>
        {failed && ended && (
          <Button
            variant="secondary"
            size="sm"
            disabled={busy}
            onClick={() => onTarget(target.node_id, "retry")}
          >
            {t("updates.page.target.retry")}
          </Button>
        )}
        {!ended && ["pending", "preparing", "prepared", "held"].includes(target.state) && (
          <Button
            variant="secondary"
            size="sm"
            disabled={busy}
            onClick={() => onTarget(target.node_id, "skip")}
          >
            {t("updates.page.target.skip")}
          </Button>
        )}
        {target.state === "healthy" && (
          <Button
            variant="secondary"
            size="sm"
            disabled={busy}
            onClick={() => onTarget(target.node_id, "rollback")}
          >
            {t("updates.page.target.rollback")}
          </Button>
        )}
      </td>
    </tr>
  );
}
