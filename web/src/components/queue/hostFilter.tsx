// The Processing pages filtered by machine (?host=<node id | home>), the
// target of the Machines page's "View in queues": which workers run on the
// machine, whether a job runs there, and the "Running on <machine>" panel
// with each job's progress (the progress bars the Machines page no longer
// draws).
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi, type JobRef, type WorkerHost } from "../../lib/queueApi";
import type { JobStreamState, LiveRun } from "../../lib/useQueueStream";
import { RunningJobs } from "./RunningJobs";
import { useThrottledReload } from "./useThrottledReload";

export const HOST_HOME = "home";
export const HOST_POLL_MS = 10_000;

const IN_FLIGHT = new Set(["claimed", "running", "resuming"]);

/** The worker ids on a machine: the home's own workers for "home", else the node's. */
export function hostWorkerIds(workers: WorkerHost[] | null, host: string): Set<string> {
  return new Set(
    (workers ?? [])
      .filter((w) => (host === HOST_HOME ? !w.node_id : w.node_id === host))
      .map((w) => w.worker_id)
  );
}

/** true when the job runs on the machine (its claimed_by, else the hub's live run). */
export function runsOnHost(
  job: Pick<JobRef, "claimed_by">,
  run: Pick<LiveRun, "worker_id"> | undefined,
  ids: ReadonlySet<string>
): boolean {
  const by = job.claimed_by ?? run?.worker_id ?? null;
  return by !== null && ids.has(by);
}

/**
 * The machine's name for headings: "this home", the node's name from the
 * workers, else the name the link carried (?name=), else its id.
 */
export function useHostName(host: string, workers: WorkerHost[] | null): string {
  const { t } = useI18n();
  const given = useSearchParams()[0].get("name");
  if (host === HOST_HOME) return t("queue.host.home");
  return workers?.find((w) => w.node_id === host && w.node_name)?.node_name ?? given ?? host;
}

/** The jobs running on a machine: the REST list, kept current by the job stream. */
export function useHostJobs(
  host: string,
  stream: JobStreamState,
  workers: WorkerHost[] | null
): JobRef[] | null {
  const [rest, setRest] = useState<JobRef[] | null>(null);
  const load = useCallback(() => {
    queueApi
      .running(host)
      .then((r) => setRest(r.jobs))
      .catch(() => setRest((prev) => prev ?? []));
  }, [host]);
  useEffect(() => {
    setRest(null);
    load();
    const id = setInterval(load, HOST_POLL_MS);
    return () => clearInterval(id);
  }, [load]);
  useThrottledReload(load, stream.finishedTick, 3_000);

  const ids = useMemo(() => hostWorkerIds(workers, host), [workers, host]);
  return useMemo(() => {
    if (rest === null) return null;
    const byId = new Map(rest.map((j) => [j.id, j]));
    for (const run of stream.running.values())
      if (runsOnHost(run.job, run, ids))
        byId.set(run.job.id, { ...byId.get(run.job.id), ...run.job });
    for (const [id, j] of stream.jobs) {
      if (!byId.has(id)) continue;
      if (IN_FLIGHT.has(j.state)) byId.set(id, { ...byId.get(id), ...j });
      else byId.delete(id);
    }
    // a fixed order: oldest first, then id (never by progress)
    return [...byId.values()].sort(
      (a, b) => (a.started_at ?? "").localeCompare(b.started_at ?? "") || a.id - b.id
    );
  }, [rest, stream.running, stream.jobs, ids]);
}

/** "Running on Studio Mac": the machine's jobs with their progress, and the way back. */
export function HostJobs({
  host,
  stream,
  workers,
}: {
  host: string;
  stream: JobStreamState;
  workers: WorkerHost[] | null;
}) {
  const { t } = useI18n();
  const name = useHostName(host, workers);
  const jobs = useHostJobs(host, stream, workers);
  return (
    <section className="mkt-q-panel mkt-q-host" aria-labelledby="mkt-q-host-h">
      <div className="mkt-q-host__head">
        <h2 className="mkt-q-section-title" id="mkt-q-host-h">
          {t("queue.host.title", { machine: bidiIsolate(name) })}
        </h2>
        <Link to="/queue">{t("queue.host.all")}</Link>
      </div>
      {jobs === null ? (
        <p className="mkt-loading" role="status">
          {t("common.loading")}
        </p>
      ) : jobs.length === 0 ? (
        <p className="mkt-q-empty">{t("queue.host.none", { machine: bidiIsolate(name) })}</p>
      ) : (
        <RunningJobs jobs={jobs} />
      )}
    </section>
  );
}
