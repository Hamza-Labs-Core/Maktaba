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
import { hrefOf, updateCodeHelp } from "../../help/registry";
import { UpdatesBackups } from "./UpdatesBackups";
import { accessAnswerWords, type HomeDb } from "../../lib/homeDb";
import {
  logsExportURL,
  logsLink,
  logWindow,
  nodeLogsLink,
  type LogFilters,
} from "../../lib/logLinks";
import {
  APP_ORDER,
  APP_ORDERED,
  APP_STAGED,
  ATTEMPT_FAILED,
  compareVersions,
  RUN_ENDED,
  runCancellable,
  TARGET_RETRYABLE,
  TARGET_SKIPPABLE,
  updatesApi,
  validVersion,
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
    case "done_needs_person":
    case "cancelled":
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
    word,
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

  // the logs of an attempt's window (a node's: its own log files, over its channel)
  const downloadLogs = async (f: LogFilters) => {
    setBusy(true);
    try {
      await downloadBlob(logsExportURL(f), "maktaba-diagnostics.tar.gz");
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
  // why a due release does not start now: a blocker, a retry's pause, a person's failure
  const tick = ov.last_tick;
  const tickWhy =
    tick && !running && tick.reason && (tick.action === "blocked" || tick.action === "none")
      ? tick.action === "blocked"
        ? w.word("blocker", tick.reason)
        : w.waiting(tick.reason)
      : "";
  const blocked = ov.catalog.filter((r) => r.blocked_reason && !r.withdrawn_at);
  const last = ov.last_run;
  const lastFailed =
    !running &&
    last &&
    (last.state === "failed" || last.state === "rolled_back") &&
    !blocked.some((r) => r.version === last.version) &&
    (last.trigger === "auto" || last.trigger === "manual");
  const lastCode = (() => {
    const e = last?.error as { code?: string } | undefined;
    return e?.code ?? last?.state ?? "";
  })();
  const installNow = (version: string) =>
    act(() => updatesApi.runNow(version), t("updates.held.started", { version }));

  return (
    <section className="mkt-page mkt-updates" aria-labelledby="updates-page-title">
      <h1 id="updates-page-title">{t("updates.page.title")}</h1>
      <p data-testid="updates-cluster-version">
        {t("updates.page.version", { version: ov.watermark || ov.current_version })}
      </p>
      <p className="mkt-muted" data-testid="updates-next">
        {next}
      </p>
      {tickWhy && target && (
        <p className="mkt-muted" data-testid="updates-tick">
          {t("updates.tick.notStarted", { reason: tickWhy })}
        </p>
      )}
      {paused && (
        <p className="mkt-muted" data-testid="updates-paused">
          {t("updates.page.paused")}
        </p>
      )}
      {ov.stuck && !running && (
        <div className="mkt-callout" role="alert" data-testid="updates-stuck">
          <p>
            {t("updates.stuck.title", {
              version: ov.stuck.version,
              windows: ov.stuck.windows,
              reason: w.reason(ov.stuck.code),
            })}
          </p>
          <p className="mkt-muted">{t("updates.stuck.body")}</p>
          <Button variant="primary" disabled={busy} onClick={() => installNow(ov.stuck!.version)}>
            {t("updates.blocked.install")}
          </Button>
        </div>
      )}
      {blocked.map((r) => (
        <div className="mkt-callout" key={r.version} data-testid={`updates-blocked-${r.version}`}>
          <p>
            {t("updates.blocked.title", {
              version: r.version,
              reason: w.word("blocked.reason", r.blocked_reason ?? ""),
            })}
          </p>
          <Button
            variant="secondary"
            disabled={busy || running}
            onClick={() =>
              act(
                () => updatesApi.unblock(r.version),
                t("updates.blocked.unblocked", { version: r.version })
              )
            }
          >
            {t("updates.blocked.unblock")}
          </Button>
          <Button
            variant="primary"
            disabled={busy || running}
            onClick={() => installNow(r.version)}
          >
            {t("updates.blocked.install")}
          </Button>
        </div>
      ))}
      {lastFailed && last && (
        <div className="mkt-callout" data-testid="updates-last-failed">
          <p>
            {t("updates.lastRun.failed", { version: last.version, reason: w.reason(lastCode) })}
          </p>
          <Button variant="secondary" disabled={busy} onClick={() => installNow(last.version)}>
            {t("updates.lastRun.retry", { version: last.version })}
          </Button>
        </div>
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
                  {m.target && APP_ORDER.has(m.state) && (
                    <div className="mkt-muted" data-testid={`updates-machine-${m.id}-app`}>
                      {t(
                        APP_STAGED.has(m.state)
                          ? "updates.page.appStaged"
                          : "updates.page.appOrdered",
                        { version: m.target }
                      )}
                    </div>
                  )}
                  {m.role === "node" && m.home_db && m.home_db.ok === false && (
                    <HomeDbNote machineId={m.id} homeDb={m.home_db} />
                  )}
                  {m.state === "worker_only" && m.convert && (
                    <div className="mkt-muted" data-testid={`updates-machine-${m.id}-convert`}>
                      {t(`updates.convert.${m.convert}`)}
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
                      onLogs={(f) => void downloadLogs(f)}
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

      <UpdatesBackups />

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

/** Where a machine's logs around a time are, on the admin log page: a joined machine's own
 * log files (over its channel), the home's live logs and log files. */
function logsHref(machine: UpdateMachine, since?: string, until?: string): string {
  return machine.role === "node"
    ? nodeLogsLink(machine.id, { since, until })
    : logsLink({ since, until });
}

/** The commands that mend a reason, as the web holds them (a machine's own text is never
 * offered to copy). */
const FIX_COMMANDS: Record<string, string> = {
  quarantined: "xattr -dr com.apple.quarantine /Applications/Maktaba.app",
  login_item_unsigned:
    "codesign --force --deep -s - --identifier com.hamzalabs.maktaba /Applications/Maktaba.app",
};

/** The reasons whose words already say the release before runs again. */
const SAYS_PREVIOUS = new Set([
  "start_failed",
  "boot_loop",
  "pending_timeout",
  "launcher_boot_loop",
  "launcher_pending_timeout",
  "launcher_refused",
]);

/** The steps an attempt fails at before its release runs (the app's check, download,
 * signature, install and relaunch; a run's preflight, stage, smoke test, backup and switch): a
 * machine that runs that release now reached it another way. A failure at a later step
 * (the pipeline's environment, the database step) happened to the release it runs: it
 * still holds. */
const BEFORE_IT_RUNS = new Set([
  "check",
  "download",
  "verify",
  "install",
  "relaunch",
  "preflight",
  "stage",
  "smoke",
  "backup",
  "switch",
]);

/** How two versions compare (above 0: a is later), null when either is not a version. */
function versionCmp(a: string, b: string): number | null {
  if (!a || !b || !validVersion(a.trim()) || !validVersion(b.trim())) return null;
  return compareVersions(a, b);
}

/** A node that cannot reach the home's database: why, the home re-provisioning its access
 * when it does, the machine's own request for one and the home's answer, what it said,
 * and its logs. */
function HomeDbNote({ machineId, homeDb }: { machineId: string; homeDb: HomeDb }) {
  const { t, locale } = useI18n();
  const code = homeDb.code ?? "other";
  const key = `machines.homeDb.code.${code}`;
  const reason =
    lookupMessage(locale, key) !== undefined ? t(key) : t("machines.homeDb.code.other");
  const answer = homeDb.answer
    ? accessAnswerWords(homeDb.answer, (k) => lookupMessage(locale, k) !== undefined)
    : null;
  const id = `updates-machine-${machineId}-home-db`;
  return (
    <div className="mkt-error" role="alert" data-testid={id}>
      <div>{t("machines.homeDb.unreachable", { reason })}</div>
      {homeDb.healing && <div className="mkt-muted">{t("machines.homeDb.healing")}</div>}
      {homeDb.asked && !homeDb.answer && !homeDb.healing && (
        <div className="mkt-muted">{t("machines.homeDb.asked")}</div>
      )}
      {homeDb.answer && answer && (
        <div data-testid={`${id}-answer`}>
          {t(answer.key, { reason: answer.reasonKey ? t(answer.reasonKey) : "" })}
        </div>
      )}
      {homeDb.answer && !homeDb.answer.ok && homeDb.answer.reason && (
        <details>
          <summary>{t("machines.homeDb.answerDetail")}</summary>
          <ErrorDetail text={homeDb.answer.reason} />
        </details>
      )}
      {homeDb.detail && (
        <details>
          <summary>{t("machines.homeDb.detail")}</summary>
          <ErrorDetail text={homeDb.detail} />
        </details>
      )}
      <a href={nodeLogsLink(machineId, logWindow(homeDb.at))} data-testid={`${id}-logs`}>
        {t("machines.homeDb.logs")}
      </a>
    </div>
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
  onLogs: (f: LogFilters) => void;
}) {
  const w = useWords();
  const { t } = w;
  const { locale } = useI18n();
  const has = (key: string) => lookupMessage(locale, key) !== undefined;
  const attempts = machine.attempts ?? [];
  const last = attempts[0];
  if (!last) return null;
  const id = `updates-machine-${machine.id}-attempt`;
  // a desktop app's server that a failed start halted on that release: it cannot go back
  // by itself (the gate put its database back and stopped it)
  const haltedOn = (a: UpdateAttempt) =>
    a.outcome === "put_back" &&
    machine.kind === "appbundle" &&
    versionCmp(machine.version, a.to) === 0;
  // the machine reached a later release, or that one another way (it failed before the
  // release ran): the failure is history; one at the release it runs, after it ran (its
  // pipeline's environment, its database step), still holds
  const cmp = versionCmp(machine.version, last.to);
  const superseded =
    ATTEMPT_FAILED.has(last.outcome) &&
    cmp !== null &&
    (cmp > 0 || (cmp === 0 && BEFORE_IT_RUNS.has(last.step ?? "") && !haltedOn(last)));
  const failed = ATTEMPT_FAILED.has(last.outcome) && !superseded;
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
        if (haltedOn(a)) {
          return t("updates.attempt.putBackStopped", {
            ...vars,
            from: a.from || t("updates.attempt.before"),
          });
        }
        return t(
          SAYS_PREVIOUS.has(a.code ?? "")
            ? "updates.attempt.putBackSaid"
            : "updates.attempt.putBack",
          vars
        );
      case "rolled_back":
        return t(
          SAYS_PREVIOUS.has(a.code ?? "")
            ? "updates.attempt.rolledBackSaid"
            : "updates.attempt.rolledBack",
          vars
        );
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
  // its logs: from two minutes before it began to two after it ended
  const win = logWindow(last.at, last.ended_at);
  // what to do next: the reason's words (and the command the web holds for it), else the
  // machine's own text, shown as what it said
  const fixKey = last.code ? `updates.fix.${last.code}` : "";
  const fixWords = fixKey && has(fixKey) ? t(fixKey) : "";
  const command = last.code ? FIX_COMMANDS[last.code] : undefined;
  return (
    <div className="mkt-updates__attempt" data-testid={id}>
      <div>
        <strong>{t("updates.attempt.title")}</strong>{" "}
        {superseded ? (
          <Badge tone="neutral">{t("updates.attempt.reached")}</Badge>
        ) : (
          <Badge tone={outcomeTone(last.outcome)}>{w.outcome(last.outcome)}</Badge>
        )}
      </div>
      <p className={failed ? "mkt-error" : undefined} data-testid={`${id}-headline`}>
        {headline(last)}
      </p>
      {superseded && (
        <p className="mkt-muted" data-testid={`${id}-superseded`}>
          {t("updates.attempt.reachedLater", { version: machine.version })}
        </p>
      )}
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
      {failed && (fixWords || command) ? (
        <div data-testid={`${id}-fix`}>
          {fixWords && <p>{t("updates.attempt.next", { fix: fixWords })}</p>}
          {command && <CopyField label={t("updates.attempt.fix")} value={command} />}
        </div>
      ) : null}
      {failed && !fixWords && !command && last.fix ? (
        <p className="mkt-muted" data-testid={`${id}-machine-fix`}>
          {t("updates.attempt.machineSays")} <span dir="ltr">{last.fix}</span>
        </p>
      ) : null}
      {failed && (
        <div className="mkt-updates__actions">
          {retry && (
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => onRetry(retry)}>
              {t("updates.page.target.retry")}
            </Button>
          )}
          {last.code ? (
            <a
              className="mkt-helplink"
              href={hrefOf(updateCodeHelp(last.code))}
              data-testid={`${id}-help`}
            >
              {t("help.learnMore")}
            </a>
          ) : null}
          <a href={logsHref(machine, win.since, win.until)} data-testid={`${id}-logs-link`}>
            {t("updates.attempt.seeLogs")}
          </a>
          <Button
            variant="secondary"
            size="sm"
            disabled={busy}
            onClick={() =>
              onLogs({ ...win, node: machine.role === "node" ? machine.id : undefined })
            }
          >
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
  // a run an admin cancelled, or a newer release replaced, did not fail; nor did one whose
  // only machines left need a person (their reasons are named)
  const failedRun =
    ended && !["done", "done_needs_person", "cancelled", "superseded"].includes(view.state);
  const needYou =
    view.state === "done_needs_person"
      ? view.targets.filter((x) => x.state === "needs_person")
      : [];
  return (
    <section aria-labelledby="updates-run-title" data-testid="updates-run">
      <h2 id="updates-run-title">
        {live
          ? t("updates.page.runLive", { version: view.version })
          : t("updates.page.runLast", { version: view.version })}
      </h2>
      <p>
        <Badge tone={stateTone(view.state)}>{w.state(view.state)}</Badge>{" "}
        {needYou.length > 0 && (
          <span className="mkt-muted" data-testid="updates-run-needs-person">
            {t("updates.page.runNeedsPerson", {
              count: needYou.length,
              machines: needYou
                .map((x) => {
                  const code = (x.error as { code?: string } | undefined)?.code;
                  return code ? `${x.name} (${w.code(String(code))})` : x.name;
                })
                .join(", "),
            })}
          </span>
        )}
        {view.detail?.waiting && (
          <span className="mkt-muted">{w.waiting(view.detail.waiting)}</span>
        )}
        {!ended && (view.step === "waiting_app" || view.step === "waiting_click") && (
          <span className="mkt-muted" data-testid="updates-run-waiting-app">
            {t("updates.page.runWaitingApp", {
              machines: [
                ...(view.state === "home_applying" ? [t("updates.page.home")] : []),
                ...view.targets.filter((x) => APP_ORDERED.has(x.state)).map((x) => x.name),
              ].join(", "),
            })}
          </span>
        )}
      </p>
      {view.error?.code && (
        <div className={failedRun ? "mkt-error" : "mkt-muted"} data-testid="updates-run-error">
          {w.code(String(view.error.code))}{" "}
          <a
            className="mkt-helplink"
            href={hrefOf(updateCodeHelp(String(view.error.code)))}
            data-testid="updates-run-error-help"
          >
            {t("help.learnMore")}
          </a>
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
            {w.code(String(target.error.code))}{" "}
            <a
              className="mkt-helplink"
              href={hrefOf(updateCodeHelp(String(target.error.code)))}
              data-testid={`updates-target-${id}-error-help`}
            >
              {t("help.learnMore")}
            </a>
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
