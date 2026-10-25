// /queue — the queue control centre (plan 06-11 §9). Health strip, the
// stage flow in lanes, "Needs attention" + live activity, libraries.
// ?host=<node id | home> (the Machines page's "View in queues") adds the
// jobs running on that machine, with their progress, and narrows the
// lanes' live runs to it.
import { useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type {
  Extension,
  FailureGroup,
  HealthSeries,
  JobRef,
  LibraryQueueRow,
} from "../../lib/queueApi";
import { runMachine, useJobStream, useQueuesSnapshot, useWorkers } from "../../lib/useQueueStream";
import { ActivityLog } from "../../components/queue/ActivityLog";
import { FlowLanes } from "../../components/queue/FlowLanes";
import { HostJobs, hostWorkerIds, runsOnHost } from "../../components/queue/hostFilter";
import { ExplainPanel } from "../../components/queue/ExplainPanel";
import { HealthStrip } from "../../components/queue/HealthStrip";
import { InsightsPanel } from "../../components/queue/InsightsPanel";
import { IssueList, groupKey } from "../../components/queue/IssueList";
import { LibrariesTable } from "../../components/queue/LibrariesTable";
import { PulseBanner } from "../../components/queue/PulseBanner";
import { RunPanel } from "../../components/queue/RunPanel";
import { StylePanel } from "../../components/queue/StylePanel";
import { useIsAdmin } from "../../components/queue/useIsAdmin";
import { WorkersStrip } from "../../components/queue/WorkersStrip";
import { QueueHeader, overallState } from "../../components/queue/QueueHeader";
import { RetriggerDialog, type RetriggerSubject } from "../../components/queue/RetriggerDialog";
import { useAction } from "../../components/queue/useAction";
import { useThrottledReload } from "../../components/queue/useThrottledReload";
import { safeGroups, safeLibraries, safeSnapshot } from "./safeData";
import "../../styles/queue.css";

export function QueueOverview() {
  const { t, n } = useI18n();
  const { snapshot: rawSnapshot, error: loadError, polling, reload } = useQueuesSnapshot();
  // a payload that is not a snapshot (an HTML page from a proxy) counts as a load error
  const snapshot = useMemo(
    () => (rawSnapshot === null ? null : safeSnapshot(rawSnapshot)),
    [rawSnapshot]
  );
  const error = loadError || (rawSnapshot !== null && snapshot === null);
  const stream = useJobStream();
  const action = useAction();
  const admin = useIsAdmin();
  const [issues, setIssues] = useState<FailureGroup[]>([]);
  const [libraries, setLibraries] = useState<LibraryQueueRow[]>([]);
  const [extensions, setExtensions] = useState<Extension[] | null>(null);
  // an older /api/workers without the key leaves undefined: treat as unknown
  const workers = useWorkers() ?? null;
  const host = useSearchParams()[0].get("host") || null;
  const [history, setHistory] = useState<HealthSeries[] | null>(null);
  const [dialog, setDialog] = useState<{ open: boolean; subjects?: RetriggerSubject[] }>({
    open: false,
  });

  const loadIssues = useCallback(() => {
    queueApi
      .issues()
      .then((r) => setIssues(safeGroups(r?.groups)))
      .catch(() => {});
  }, []);
  const loadLibraries = useCallback(() => {
    queueApi
      .libraries()
      .then((r) => setLibraries(safeLibraries(r?.items)))
      .catch(() => {});
  }, []);

  useEffect(() => {
    loadIssues();
    loadLibraries();
  }, [loadIssues, loadLibraries]);

  useEffect(() => {
    queueApi
      .extensions()
      .then((r) => setExtensions(r?.extensions ?? null))
      .catch(() => {});
  }, []);

  // each queue's jobs running now, wherever they run (the hub's job events)
  const liveRuns = useMemo(() => {
    const m = new Map<string, { label: string; machine: string | null; job: JobRef }[]>();
    const ids = host ? hostWorkerIds(workers, host) : null;
    for (const run of stream.running.values()) {
      if (ids && !runsOnHost(run.job, run, ids)) continue;
      const list = m.get(run.job.stage) ?? [];
      list.push({ label: run.job.subject_label, machine: runMachine(run, workers), job: run.job });
      m.set(run.job.stage, list);
    }
    return m;
  }, [stream.running, workers, host]);

  // 4 h sparklines: refresh once a minute (the snapshot only carries ~30 min).
  useEffect(() => {
    let alive = true;
    const load = () =>
      queueApi
        .health(4)
        .then((r) => alive && setHistory(Array.isArray(r?.series) ? r.series : null))
        .catch(() => {});
    load();
    const id = setInterval(load, 60_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  useThrottledReload(loadIssues, stream.finishedTick);
  useThrottledReload(loadLibraries, stream.finishedTick, 15_000);
  useEffect(() => {
    if (snapshot) loadIssues();
  }, [snapshot?.issues_count]);

  const stageLabel = useMemo(() => {
    const m = new Map((snapshot?.stages ?? []).map((s) => [s.stage, s.label]));
    return (s: string) => m.get(s) ?? s;
  }, [snapshot?.stages]);

  if (!snapshot) {
    return (
      <section className="mkt-page mkt-q">
        {error ? (
          <div role="alert" className="mkt-q-banner">
            <p>{t("queue.error.load")}</p>
            <Button variant="secondary" onClick={reload}>
              {t("queue.error.retry")}
            </Button>
          </div>
        ) : (
          <p className="mkt-loading" role="status">
            {t("common.loading")}
          </p>
        )}
      </section>
    );
  }

  const running = snapshot.queues.reduce((a, q) => a + q.counts.running, 0);
  const state = overallState({
    pausedAll: snapshot.paused_all,
    issues: Math.max(snapshot.issues_count, issues.length),
    running,
  });

  const refreshAfter = () => {
    reload();
    loadIssues();
    loadLibraries();
  };

  const onRetry = (g: FailureGroup) =>
    void action
      .run(groupKey(g), () => queueApi.retry(g.stage, { cause: g.cause }))
      .then((r) => r && refreshAfter());
  // every group with a Retry button, one retry each (the same call as the button)
  const retryable = issues.filter((g) => g.action === "retry");
  const retryableCount = retryable.reduce((sum, g) => sum + g.count, 0);
  const onRetryAll = () =>
    void action
      .run("retry-all", async () => {
        for (const g of retryable) await queueApi.retry(g.stage, { cause: g.cause });
        return true;
      })
      .then((r) => r && refreshAfter());
  const onApply = (g: FailureGroup) =>
    void action
      .run(groupKey(g), () => queueApi.apply(g.stage, { all: true }))
      .then((r) => r && refreshAfter());

  const openDialog = (rows: LibraryQueueRow[]) =>
    setDialog({
      open: true,
      subjects: rows.map((r) => ({ kind: "library", id: r.library_id, label: r.name })),
    });

  return (
    <section className="mkt-page mkt-q">
      <QueueHeader
        state={state}
        pausedAll={snapshot.paused_all}
        busy={action.busy === "pause-all"}
        retryAll={
          retryable.length > 0
            ? {
                count: retryableCount,
                busy: action.busy === "retry-all",
                disabled: action.busy != null && action.busy !== "retry-all",
                onClick: onRetryAll,
              }
            : undefined
        }
        onTogglePause={() =>
          void action
            .run("pause-all", () =>
              snapshot.paused_all ? queueApi.resumeAll() : queueApi.pauseAll()
            )
            .then(() => reload())
        }
        onQueueWork={() => setDialog({ open: true })}
      />
      {action.error && (
        <p role="alert" className="mkt-q-banner">
          {action.error}
        </p>
      )}
      {polling && (
        <p role="status" className="mkt-q-note">
          {t("queue.polling")}
        </p>
      )}

      {host && <HostJobs host={host} stream={stream} workers={workers} />}

      <PulseBanner />

      <RunPanel
        stages={snapshot.stages}
        libraries={libraries}
        extensions={extensions}
        canRun={admin}
        onDone={() => {
          refreshAfter();
          queueApi
            .extensions()
            .then((r) => setExtensions(r?.extensions ?? null))
            .catch(() => {});
        }}
      />

      <HealthStrip health={snapshot.health} history={history} />

      <WorkersStrip workers={workers} />

      <InsightsPanel />

      <FlowLanes
        stages={snapshot.stages}
        queues={snapshot.queues}
        pools={snapshot.pools}
        flows={snapshot.flows}
        extensions={extensions}
        live={liveRuns}
      />

      <div className="mkt-q-cols">
        <section className="mkt-q-panel" aria-labelledby="mkt-q-issues-h">
          <div className="mkt-q-section-head">
            <h2 className="mkt-q-section-title" id="mkt-q-issues-h">
              {t("queue.issues.title")}
            </h2>
            {retryable.length > 1 && (
              <Button
                size="sm"
                variant="secondary"
                loading={action.busy === "retry-all"}
                disabled={action.busy != null && action.busy !== "retry-all"}
                onClick={onRetryAll}
              >
                {t("queue.issues.retryAll", { n: n(retryableCount) })}
              </Button>
            )}
          </div>
          <IssueList
            groups={issues}
            stageLabel={stageLabel}
            busyKey={action.busy}
            onRetry={onRetry}
            onApply={onApply}
            nasP95Ms={snapshot.health.nas.open_ms_p95}
          />
        </section>
        <section className="mkt-q-panel" aria-labelledby="mkt-q-log-h">
          <h2 className="mkt-q-section-title" id="mkt-q-log-h">
            {t("queue.log.title")}
          </h2>
          <ActivityLog stages={snapshot.stages} live={stream.lines} />
        </section>
      </div>

      <LibrariesTable rows={libraries} stages={snapshot.stages} onRequeue={openDialog} />

      <div className="mkt-q-cols">
        <div className="mkt-q-panel">
          <ExplainPanel libraries={libraries} />
        </div>
        <div className="mkt-q-panel">
          <StylePanel libraries={libraries} canEdit={admin} />
        </div>
      </div>

      <RetriggerDialog
        open={dialog.open}
        onClose={() => setDialog({ open: false })}
        stages={snapshot.stages}
        libraries={libraries}
        initialSubjects={dialog.subjects}
        onDone={refreshAfter}
      />
    </section>
  );
}
