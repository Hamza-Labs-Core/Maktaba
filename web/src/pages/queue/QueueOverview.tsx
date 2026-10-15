// /queue — the queue control centre (plan 06-11 §9). Health strip, the
// stage flow in lanes, "Needs attention" + live activity, libraries.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type {
  Extension,
  FailureGroup,
  HealthSeries,
  LibraryQueueRow,
  WorkerHost,
} from "../../lib/queueApi";
import { useJobStream, useQueuesSnapshot } from "../../lib/useQueueStream";
import { ActivityLog } from "../../components/queue/ActivityLog";
import { FlowLanes } from "../../components/queue/FlowLanes";
import { HealthStrip } from "../../components/queue/HealthStrip";
import { IssueList, groupKey } from "../../components/queue/IssueList";
import { LibrariesTable } from "../../components/queue/LibrariesTable";
import { WorkersStrip } from "../../components/queue/WorkersStrip";
import { QueueHeader, overallState } from "../../components/queue/QueueHeader";
import { RetriggerDialog, type RetriggerSubject } from "../../components/queue/RetriggerDialog";
import { useAction } from "../../components/queue/useAction";
import { useThrottledReload } from "../../components/queue/useThrottledReload";
import "../../styles/queue.css";

export function QueueOverview() {
  const { t } = useI18n();
  const { snapshot, error, polling, reload } = useQueuesSnapshot();
  const stream = useJobStream();
  const action = useAction();
  const [issues, setIssues] = useState<FailureGroup[]>([]);
  const [libraries, setLibraries] = useState<LibraryQueueRow[]>([]);
  const [extensions, setExtensions] = useState<Extension[] | null>(null);
  const [workers, setWorkers] = useState<WorkerHost[] | null>(null);
  const [history, setHistory] = useState<HealthSeries[] | null>(null);
  const [dialog, setDialog] = useState<{ open: boolean; subjects?: RetriggerSubject[] }>({
    open: false,
  });

  const loadIssues = useCallback(() => {
    queueApi
      .issues()
      .then((r) => setIssues(r.groups))
      .catch(() => {});
  }, []);
  const loadLibraries = useCallback(() => {
    queueApi
      .libraries()
      .then((r) => setLibraries(r.items))
      .catch(() => {});
  }, []);

  useEffect(() => {
    loadIssues();
    loadLibraries();
  }, [loadIssues, loadLibraries]);

  useEffect(() => {
    queueApi
      .extensions()
      .then((r) => setExtensions(r.extensions))
      .catch(() => {});
  }, []);

  useEffect(() => {
    let alive = true;
    const load = () =>
      queueApi
        .workers()
        .then((r) => alive && setWorkers(r.workers))
        .catch(() => {});
    load();
    const id = setInterval(load, 30_000);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, []);

  // 4 h sparklines: refresh once a minute (the snapshot only carries ~30 min).
  useEffect(() => {
    let alive = true;
    const load = () =>
      queueApi
        .health(4)
        .then((r) => alive && setHistory(r.series))
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

      <HealthStrip health={snapshot.health} history={history} />

      <WorkersStrip workers={workers} />

      <FlowLanes
        stages={snapshot.stages}
        queues={snapshot.queues}
        pools={snapshot.pools}
        flows={snapshot.flows}
        extensions={extensions}
      />

      <div className="mkt-q-cols">
        <section className="mkt-q-panel" aria-labelledby="mkt-q-issues-h">
          <h2 className="mkt-q-section-title" id="mkt-q-issues-h">
            {t("queue.issues.title")}
          </h2>
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
