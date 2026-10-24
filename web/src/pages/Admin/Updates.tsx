// Admin -> Updates (Epic 35, story 35.13; install-kinds.md): the cluster's automatic
// updates on one page. Updates install by themselves, so the page is mostly for reading:
// every machine's version and install kind, the next planned update (or why there is
// none), the run under way step by step (the home's steps, then one row per machine),
// and what happened before. The few actions are here too: Update now, Check now, pause
// (automatic installs off), Cancel a run before the home switches, Retry, Skip or Roll
// back a machine, Resume a held rollout. Under each machine, its last update attempt
// (update-attempts.md): whoever made it (the desktop app's own updater, a release's first
// start, a run), what happened, why, and what to do next.
import { Fragment, useCallback, useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError, downloadBlob } from "../../lib/api";
import { lookupMessage, useI18n } from "../../lib/i18n";
import { CopyField } from "../../components/CopyField";
import {
  ATTEMPT_FAILED,
  RUN_ENDED,
  runCancellable,
  TARGET_RETRYABLE,
  TARGET_SKIPPABLE,
  WAITING_CLICK,
  updatesApi,
  type UpdateAttempt,
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
    case "prepare_failed":
    case "apply_failed":
      return "error";
    case "held":
    case "behind":
    case "offline":
    case "worker_only":
    case "ahead_of_home":
    case "done_with_failures":
    case "cancelled":
    case "waiting_click":
    case "awaiting_click":
    case "needs_person":
      return "warn";
    case "superseded":
    case "aborted":
      return "neutral";
    default:
      return "accent";
  }
}

/** The page's words for what the API sends (states, steps, install kinds, triggers, error
 * codes), falling back gracefully for a value it has none for (a newer server): the value
 * itself, its underscores as spaces. */
function useWords() {
  const { t, locale } = useI18n();
  const has = (key: string) => lookupMessage(locale, key) !== undefined;
  const word = (group: string, value: string) => {
    const key = `updates.${group}.${value}`;
    return has(key) ? t(key) : value.replace(/_/g, " ");
  };
  const state = (s: string) => word("state", s);
  const step = (s: string) => word("step", s);
  /** An error code's words: a check of the release the verification's, a step's failure
   * ("stage_failed") the step's; any other "Stopped at: <code>". */
  const code = (c: string) => {
    if (has(`updates.code.${c}`)) return t(`updates.code.${c}`);
    if (c.startsWith("verify_")) return t("updates.code.verify_failed");
    const failedStep = /^([a-z]+)_failed$/.exec(c)?.[1];
    if (failedStep && has(`updates.step.${failedStep}`)) {
      return `${step(failedStep)}: ${state("failed")}`;
    }
    return t("updates.page.failedCheck", { code: c });
  };
  /** An attempt's reason: the code's words as code() has them, else the code itself, its
   * underscores as spaces (the detail beside it says the rest). */
  const reason = (c: string) => {
    const words = code(c);
    return words === t("updates.page.failedCheck", { code: c }) ? c.replace(/_/g, " ") : words;
  };
  return {
    t,
    state,
    step,
    code,
    reason,
    outcome: (o: string) => word("outcome", o),
    source: (src: string) => word("source", src),
    kind: (k: string) => word("kind", k),
    trigger: (s: string) => word("trigger", s),
    waiting: (s: string) => word("waiting", s),
  };
}

/** An error's detail as the server words it (English): it can carry the fix, a `command`
 * in it set as code to copy. */
function ErrorDetail({ text, testId }: { text: string; testId?: string }) {
  return (
    <div className="mkt-muted" dir="ltr" data-testid={testId}>
      {text.split("`").map((part, i) => (i % 2 === 1 ? <code key={i}>{part}</code> : part))}
    </div>
  );
}

function when(iso?: string): string {
  return iso ? new Date(iso).toLocaleString() : "";
}

/** How long ago, in the reader's language ("5 minutes ago"). */
function ago(iso: string, locale: string): string {
  const s = (Date.parse(iso) - Date.now()) / 1000;
  if (!Number.isFinite(s)) return "";
  const rtf = new Intl.RelativeTimeFormat(locale === "ar" ? "ar-EG" : "en", { numeric: "auto" });
  const a = Math.abs(s);
  if (a < 60) return rtf.format(Math.round(s), "second");
  if (a < 3600) return rtf.format(Math.round(s / 60), "minute");
  if (a < 86400) return rtf.format(Math.round(s / 3600), "hour");
  return rtf.format(Math.round(s / 86400), "day");
}

/** An attempt's badge tone. */
function outcomeTone(outcome: string): Tone {
  if (outcome === "installed") return "success";
  if (ATTEMPT_FAILED.has(outcome)) return "error";
  if (outcome === "cancelled") return "neutral";
  return "accent";
}

function duration(run: UpdateRun): string {
  if (!run.finished_at) return "";
  const s = Math.max(0, (Date.parse(run.finished_at) - Date.parse(run.started_at)) / 1000);
  return s < 120 ? `${Math.round(s)} s` : `${Math.round(s / 60)} min`;
}

export function AdminUpdates() {
  const toast = useToast();
  const w = useWords();
  const { t } = w;
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

  const downloadLogs = async () => {
    setBusy(true);
    try {
      await downloadBlob("/api/diagnostics/export", "maktaba-diagnostics.tar.gz");
    } catch (e) {
      const detail = e instanceof ApiError ? e.message : t("common.error");
      toast.show({ tone: "error", message: `${t("updates.attempt.logsFailed")}: ${detail}` });
    } finally {
      setBusy(false);
    }
  };

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
            <Fragment key={m.id}>
              <tr data-testid={`updates-machine-${m.id}`}>
                <td>
                  {m.role === "home" ? t("updates.page.home") : m.name}
                  {m.platform ? <span className="mkt-muted"> · {m.platform}</span> : null}
                </td>
                <td dir="ltr">{m.version || "-"}</td>
                <td>{m.kind ? w.kind(m.kind) : "-"}</td>
                <td>
                  <Badge tone={stateTone(m.state)}>{w.state(m.state)}</Badge>
                  {m.target && WAITING_CLICK.has(m.state) && (
                    <div className="mkt-muted" data-testid={`updates-machine-${m.id}-click`}>
                      {t(
                        m.state === "clicked"
                          ? "updates.page.clicked"
                          : "updates.page.waitingClick",
                        {
                          version: m.target,
                        }
                      )}
                    </div>
                  )}
                </td>
              </tr>
              {(m.attempts?.length ?? 0) > 0 && (
                <tr className="mkt-updates__attempt-row">
                  <td colSpan={4}>
                    <AttemptBlock
                      machine={m}
                      busy={busy}
                      onRetry={(runId) =>
                        act(
                          () => updatesApi.targetAction(runId, m.id, "retry"),
                          t("updates.page.target.retryDone")
                        )
                      }
                      onLogs={() => void downloadLogs()}
                    />
                  </td>
                </tr>
              )}
            </Fragment>
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
                <td>{w.trigger(r.trigger)}</td>
                <td>
                  <Badge tone={stateTone(r.state)}>{w.state(r.state)}</Badge>
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

/** A machine's last update attempt, then its earlier ones (update-attempts.md). */
function AttemptBlock({
  machine,
  busy,
  onRetry,
  onLogs,
}: {
  machine: UpdateMachine;
  busy: boolean;
  onRetry: (runId: string) => void;
  onLogs: () => void;
}) {
  const w = useWords();
  const { t } = w;
  const { locale } = useI18n();
  const attempts = machine.attempts ?? [];
  const last = attempts[0];
  if (!last) return null;
  const id = `updates-machine-${machine.id}-attempt`;
  const failed = ATTEMPT_FAILED.has(last.outcome);
  const reasonOf = (a: UpdateAttempt) =>
    a.code
      ? w.reason(a.code)
      : a.detail
        ? (a.detail.split("\n")[0] ?? a.detail)
        : t("updates.attempt.noReason");
  const headline = (a: UpdateAttempt) => {
    // a check that failed before the feed named a release has no `to`
    const to = a.to || t("updates.attempt.newRelease");
    const vars = { to, reason: reasonOf(a) };
    switch (a.outcome) {
      case "installed":
        return t("updates.attempt.installed", vars);
      case "running":
        return t("updates.attempt.running", vars);
      case "failed":
        return t("updates.attempt.failed", vars);
      case "put_back":
        return t("updates.attempt.putBack", vars);
      case "rolled_back":
        return t("updates.attempt.rolledBack", vars);
      case "cancelled":
        return t("updates.attempt.cancelled", vars);
      default:
        return t("updates.attempt.other", { to, outcome: w.outcome(a.outcome) });
    }
  };
  const who = last.trigger
    ? t("updates.attempt.byTrigger", {
        who: w.source(last.source),
        how: w.trigger(last.trigger),
      })
    : t("updates.attempt.by", { who: w.source(last.source) });
  // a run's machine is retried as the run page's Retry does (the home has Update now)
  const retry =
    failed && last.source === "run" && last.run_id && machine.role === "node" ? last.run_id : "";
  return (
    <div className="mkt-updates__attempt" data-testid={id}>
      <div>
        <strong>{t("updates.attempt.title")}</strong>{" "}
        <Badge tone={outcomeTone(last.outcome)}>{w.outcome(last.outcome)}</Badge>
      </div>
      <p className={failed ? "mkt-error" : undefined} data-testid={`${id}-headline`}>
        {headline(last)}
      </p>
      <p className="mkt-muted" data-testid={`${id}-meta`}>
        {t("updates.attempt.when", { ago: ago(last.at, locale), at: when(last.at) })}
        {" · "}
        {last.from || last.to ? (
          <>
            <span dir="ltr">
              {last.from ? `${last.from} → ` : ""}
              {last.to || "?"}
            </span>
            {" · "}
          </>
        ) : null}
        {who}
        {last.step ? (
          <>
            {" · "}
            {t(
              last.outcome === "running" ? "updates.attempt.atStep" : "updates.attempt.stoppedAt",
              { step: w.step(last.step) }
            )}
          </>
        ) : null}
      </p>
      {last.detail ? (
        <details data-testid={`${id}-detail`}>
          <summary>{t("updates.attempt.detail")}</summary>
          <ErrorDetail text={last.detail} />
        </details>
      ) : null}
      {last.fix ? <CopyField label={t("updates.attempt.fix")} value={last.fix} /> : null}
      {failed && (
        <div className="mkt-updates__actions">
          {retry && (
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => onRetry(retry)}>
              {t("updates.page.target.retry")}
            </Button>
          )}
          <Button variant="secondary" size="sm" disabled={busy} onClick={onLogs}>
            {t("updates.attempt.logs")}
          </Button>
        </div>
      )}
      {attempts.length > 1 && (
        <details data-testid={`${id}-earlier`}>
          <summary>{t("updates.attempt.earlier", { count: attempts.length - 1 })}</summary>
          <ul>
            {attempts.slice(1).map((a) => (
              <li key={a.id}>
                <span className="mkt-muted">{when(a.at)}</span>{" "}
                <Badge tone={outcomeTone(a.outcome)}>{w.outcome(a.outcome)}</Badge> {headline(a)}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
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
  const w = useWords();
  const { t } = w;
  const homeSteps = view.steps.filter((s) => s.host === "home");
  const ended = RUN_ENDED.has(view.state);
  const held = view.targets.some((x) => x.state === "held");
  // a run an admin cancelled, or a newer release replaced, did not fail
  const failedRun = ended && !["done", "cancelled", "superseded"].includes(view.state);
  return (
    <section aria-labelledby="updates-run-title" data-testid="updates-run">
      <h2 id="updates-run-title">
        {live
          ? t("updates.page.runLive", { version: view.version })
          : t("updates.page.runLast", { version: view.version })}
      </h2>
      <p>
        <Badge tone={stateTone(view.state)}>{w.state(view.state)}</Badge>{" "}
        {view.detail?.waiting && (
          <span className="mkt-muted">{w.waiting(view.detail.waiting)}</span>
        )}
        {!ended && view.step === "waiting_click" && (
          <span className="mkt-muted" data-testid="updates-run-waiting-click">
            {t("updates.page.runWaitingClick", {
              machines: [
                ...(view.state === "home_applying" ? [t("updates.page.home")] : []),
                ...view.targets.filter((x) => WAITING_CLICK.has(x.state)).map((x) => x.name),
              ].join(", "),
            })}
          </span>
        )}
      </p>
      {view.error?.code && (
        <div className={failedRun ? "mkt-error" : "mkt-muted"} data-testid="updates-run-error">
          {w.code(String(view.error.code))}
          {view.error.detail ? (
            <ErrorDetail text={String(view.error.detail)} testId="updates-run-error-detail" />
          ) : null}
        </div>
      )}
      <div className="mkt-updates__actions">
        {!ended && runCancellable(view) && (
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
              kind: view.install_kind ? w.kind(view.install_kind) : "",
            })}
          </h3>
          <ol className="mkt-updates__steps" data-testid="updates-home-steps">
            {homeSteps.map((s) => (
              <li key={s.seq}>
                <Badge tone={stateTone(s.state)}>{w.step(s.step)}</Badge>{" "}
                <span className="mkt-muted">
                  {w.state(s.state)}
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
  const w = useWords();
  const { t } = w;
  const id = target.node_id;
  return (
    <tr data-testid={`updates-target-${id}`}>
      <td>
        {target.name}
        {target.platform ? <span className="mkt-muted"> · {target.platform}</span> : null}
      </td>
      <td dir="ltr">
        {target.from_version} → {target.to_version}
      </td>
      <td>
        <Badge tone={stateTone(target.state)}>{w.state(target.state)}</Badge>
        {target.error?.held_by ? (
          <span className="mkt-muted">
            {" "}
            {t("updates.page.heldBy", { machine: String(target.error.held_by) })}
          </span>
        ) : target.error?.code ? (
          <span className="mkt-muted" data-testid={`updates-target-${id}-error`}>
            {" "}
            {w.code(String(target.error.code))}
          </span>
        ) : null}
        {target.error?.detail ? (
          <ErrorDetail
            text={String(target.error.detail)}
            testId={`updates-target-${id}-error-detail`}
          />
        ) : null}
      </td>
      <td>
        {ended && TARGET_RETRYABLE.has(target.state) && (
          <Button
            variant="secondary"
            size="sm"
            disabled={busy}
            onClick={() => onTarget(target.node_id, "retry")}
          >
            {t("updates.page.target.retry")}
          </Button>
        )}
        {!ended && TARGET_SKIPPABLE.has(target.state) && (
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
