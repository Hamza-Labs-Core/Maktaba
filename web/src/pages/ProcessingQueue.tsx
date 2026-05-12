// Story 11.5 — Processing queue dashboard.
//
// Subscribes to the `/ws/v1/events` stream for job state changes and
// polls `/api/jobs` on mount.
import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { ws, type WSMessage } from "../lib/ws";
import { useI18n } from "../lib/i18n";

interface Job {
  id: string;
  stage: string;
  state: string;
  video_id?: string;
  progress_pct?: number;
  error?: { type: string; message: string } | null;
}

interface JobListResponse {
  items: Job[];
}

export function ProcessingQueue() {
  const { t } = useI18n();
  const [jobs, setJobs] = useState<Job[] | null>(null);

  useEffect(() => {
    let alive = true;
    api
      .get<JobListResponse>("/api/jobs?limit=100")
      .then((res) => {
        if (alive) setJobs(res.items ?? []);
      })
      .catch(() => {
        if (alive) setJobs([]);
      });
    ws.start();
    const unsub = ws.subscribe((msg: WSMessage) => {
      if (msg.type !== "job.updated" || !msg.job) return;
      setJobs((prev) => {
        if (!prev) return prev;
        const idx = prev.findIndex((j) => j.id === msg.job.id);
        if (idx < 0) return [msg.job as Job, ...prev];
        const next = prev.slice();
        next[idx] = msg.job as Job;
        return next;
      });
    });
    return () => {
      alive = false;
      unsub();
    };
  }, []);

  if (!jobs) return <p>{t("common.loading")}</p>;
  if (jobs.length === 0) return <p className="mkt-empty">{t("common.empty")}</p>;

  return (
    <section className="mkt-page mkt-queue">
      <h1>{t("nav.queue")}</h1>
      <table className="mkt-table" aria-label={t("nav.queue")}>
        <thead>
          <tr>
            <th>Stage</th>
            <th>State</th>
            <th>Progress</th>
            <th>Video</th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((j) => (
            <tr key={j.id}>
              <td>{j.stage}</td>
              <td>
                <span className={`mkt-state mkt-state--${j.state}`}>{j.state}</span>
              </td>
              <td>
                <progress max={100} value={j.progress_pct ?? 0}>
                  {j.progress_pct ?? 0}%
                </progress>
              </td>
              <td className="mkt-mono">{j.video_id ?? "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
