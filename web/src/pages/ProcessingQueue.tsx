// Story 11.5 — Processing queue dashboard.
//
// Real contracts:
//   GET  /api/jobs?limit=          { items:[Job] }   (no progress_pct
//        in the API Job struct — we show stage/state/ETA/attempts)
//   GET  /api/queue/stats          { by_stage, eta_sec, total_in_flight,
//        oldest_pending_age_sec }
//   POST /api/jobs/{id}/(pause|resume|cancel|retry)
// Live via SSE /ws/jobs with the AC-3 {type,at,payload} envelope (the
// old code used a dead /ws/v1/events WebSocket and msg.job). Updates
// are coalesced to ≤1 re-render/second.
import { useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { api } from "../lib/api";
import { subscribe } from "../lib/ws";
import { useI18n } from "../lib/i18n";

interface Job {
  id: string;
  stage: string;
  state: string;
  video_id?: string | null;
  attempts: number;
  estimated_remaining_sec?: number | null;
  error?: string | null;
}
interface StageStats {
  pending: number;
  running: number;
  paused: number;
  failed: number;
  done_24h: number;
}
interface QueueStats {
  by_stage: Record<string, StageStats>;
  eta_sec: number;
  total_in_flight: number;
  oldest_pending_age_sec: number;
}

const STATE_TONE: Record<string, "neutral" | "accent" | "success" | "warn" | "error"> = {
  running: "accent",
  done: "success",
  completed: "success",
  failed: "error",
  paused: "warn",
  pending: "neutral",
  queued: "neutral",
};

const TS = (s: number) => {
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return `${m}:${sec.toString().padStart(2, "0")}`;
};

const ACTIVE = new Set(["pending", "queued", "running", "paused"]);

export function ProcessingQueue() {
  const { t, n } = useI18n();
  const [jobs, setJobs] = useState<Job[] | null>(null);
  const [stats, setStats] = useState<QueueStats | null>(null);
  const [showAll, setShowAll] = useState(false);
  const pendingRef = useRef<Map<string, Job>>(new Map());
  const flushTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  function load() {
    api
      .get<{ items: Job[] }>("/api/jobs?limit=200")
      .then((r) => setJobs(r.items ?? []))
      .catch(() => setJobs([]));
    api
      .get<QueueStats>("/api/queue/stats")
      .then(setStats)
      .catch(() => setStats(null));
  }

  useEffect(() => {
    load();
    // Coalesce SSE bursts to one state flush per second (AC ≤1Hz).
    const off = subscribe("jobs", (ev) => {
      const job = ev.payload?.job as Job | undefined;
      if (!job) return;
      pendingRef.current.set(job.id, job);
      if (flushTimer.current) return;
      flushTimer.current = setTimeout(() => {
        flushTimer.current = null;
        const batch = pendingRef.current;
        pendingRef.current = new Map();
        setJobs((prev) => {
          const map = new Map((prev ?? []).map((j) => [j.id, j]));
          batch.forEach((j, id) => map.set(id, j));
          return [...map.values()];
        });
      }, 1000);
    });
    return () => {
      off();
      if (flushTimer.current) clearTimeout(flushTimer.current);
    };
  }, []);

  function act(id: string, op: "pause" | "resume" | "cancel" | "retry") {
    void api
      .post(`/api/jobs/${encodeURIComponent(id)}/${op}`)
      .then(load)
      .catch(() => {});
  }

  if (!jobs) return <p className="mkt-loading">{t("common.loading")}</p>;

  const visible = showAll ? jobs : jobs.filter((j) => ACTIVE.has(j.state));

  return (
    <section className="mkt-page mkt-queue">
      <header className="mkt-page__header">
        <h1>{t("nav.queue")}</h1>
        <div className="mkt-toolbar" role="toolbar">
          <span className="mkt-muted">{showAll ? "" : t("queue.recent")}</span>
          <Button variant="secondary" onClick={() => setShowAll((v) => !v)} aria-pressed={showAll}>
            {showAll ? t("queue.recent") : t("queue.showAll")}
          </Button>
        </div>
      </header>

      {stats && (
        <div className="mkt-stage-cards">
          {Object.entries(stats.by_stage).map(([stage, s]) => (
            <Card key={stage} className="mkt-stage-card">
              <strong>{stage}</strong>
              <div className="mkt-muted">
                {t("queue.col.state")}: {n(s.running)}/{n(s.pending)} · {n(s.failed)} ✕
              </div>
            </Card>
          ))}
          <Card className="mkt-stage-card">
            <strong>{t("queue.col.eta")}</strong>
            <div className="mkt-muted">
              {TS(stats.eta_sec)} · {n(stats.total_in_flight)}
            </div>
          </Card>
        </div>
      )}

      {visible.length === 0 ? (
        <EmptyState title={t("queue.empty.title")} description={t("queue.empty.desc")} />
      ) : (
        <table className="mkt-table" aria-label={t("nav.queue")}>
          <thead>
            <tr>
              <th>{t("queue.col.stage")}</th>
              <th>{t("queue.col.state")}</th>
              <th>{t("queue.col.eta")}</th>
              <th>{t("queue.col.attempts")}</th>
              <th>{t("queue.col.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((j) => (
              <tr key={j.id}>
                <td>{j.stage}</td>
                <td>
                  <Badge tone={STATE_TONE[j.state] ?? "neutral"}>{j.state}</Badge>
                  {j.error && <span className="mkt-muted"> {j.error}</span>}
                </td>
                <td>
                  {typeof j.estimated_remaining_sec === "number"
                    ? TS(j.estimated_remaining_sec)
                    : "—"}
                </td>
                <td>{n(j.attempts)}</td>
                <td className="mkt-row-actions">
                  {j.state === "paused" ? (
                    <Button size="sm" variant="ghost" onClick={() => act(j.id, "resume")}>
                      {t("queue.action.resume")}
                    </Button>
                  ) : (
                    <Button size="sm" variant="ghost" onClick={() => act(j.id, "pause")}>
                      {t("queue.action.pause")}
                    </Button>
                  )}
                  {j.state === "failed" && (
                    <Button size="sm" variant="ghost" onClick={() => act(j.id, "retry")}>
                      {t("queue.action.retry")}
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" onClick={() => act(j.id, "cancel")}>
                    {t("queue.action.cancel")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
