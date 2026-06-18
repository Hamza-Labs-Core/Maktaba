// Story 11.2 — Video detail page.
//
// Tabs: Watch | Transcript | Chapters | Files | Processing. Real
// contracts:
//   GET /api/videos/{id}                 header
//   GET /api/videos/{id}/chapters        { items:[{seq,start_sec,title}] }
//   GET /api/videos/{id}/subtitles       { items:[{language,format,is_default,url}] }
//   GET /api/videos/{id}/segments?from=&to=  { items:[Segment], partial? }
//   GET /api/jobs                        { items:[Job] } (filtered by video)
// Live updates via SSE /ws/library/{id} (the AC-3 {type,at,payload}
// envelope). The transcript list is windowed (slice cap) rather than a
// TanStack-Virtual integration — see report deferral note.
import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { Badge } from "@ds/components/Badge/Badge";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { api, ApiError } from "../lib/api";
import { subscribe } from "../lib/ws";
import { useI18n } from "../lib/i18n";
import { MediaContextCard } from "../components/MediaContextCard";
import { EnrichmentPanel } from "../components/EnrichmentPanel";
import { useAuth } from "../lib/auth";
import {
  analyticsApi,
  formatPercent,
  formatPercentRatio,
  formatWatchTime,
  type VideoStats,
} from "../lib/analytics";

interface Video {
  id: string;
  title?: string | null;
  filename: string;
  description?: string | null;
  state: string;
  detected_language?: string | null;
  duration_sec?: number | null;
}
interface Chapter {
  id: number;
  seq: number;
  start_sec: number;
  end_sec: number;
  title: string;
}
interface Subtitle {
  id: number;
  language: string;
  format: string;
  source: string;
  is_default: boolean;
  url: string;
}
interface Segment {
  id: number;
  seq: number;
  start_sec: number;
  end_sec: number;
  text: string;
  speaker?: string | null;
}
interface Job {
  id: string;
  stage: string;
  state: string;
  video_id?: string | null;
  attempts: number;
  estimated_remaining_sec?: number | null;
  error?: string | null;
}

// Canonical pipeline stage names (Story 7.x). Unknown stages fall back
// to the raw key so a new backend stage never renders blank.
const STAGE_LABEL: Record<string, string> = {
  probe: "Probe",
  thumbnail: "Thumbnails",
  transcode: "Transcode",
  transcribe: "Transcribe",
  diarize: "Diarize",
  align: "Align",
  embed: "Embed",
  index: "Index",
};

const TS = (s: number) => {
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return `${m}:${sec.toString().padStart(2, "0")}`;
};

export function VideoDetail() {
  const { videoId } = useParams();
  const { t } = useI18n();
  const [video, setVideo] = useState<Video | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!videoId) return;
    setVideo(null);
    setErr(null);
    api
      .get<Video>(`/api/videos/${encodeURIComponent(videoId)}`)
      .then(setVideo)
      .catch((e) => setErr(e instanceof ApiError ? e.problem.title : t("common.error")));
  }, [videoId, t]);

  if (err) return <ErrorState kind="not_found" title={t("video.notFound")} description={err} />;
  if (!video || !videoId) return <p className="mkt-loading">{t("common.loading")}</p>;

  const items = [
    {
      id: "watch",
      label: t("video.tab.watch"),
      content: (
        <div className="mkt-video__watch">
          {video.description && (
            <p className="mkt-video__desc" dir="auto">
              {video.description}
            </p>
          )}
          <Link to={`/videos/${video.id}/watch`} className="mkt-btn mkt-btn--primary">
            ▶ {t("video.play")}
          </Link>
          {/* Story 26.9 — web context card (rating/cast/summary + related). */}
          <MediaContextCard videoId={videoId} />
        </div>
      ),
    },
    {
      id: "enrichment",
      label: t("video.tab.metadata"),
      content: <EnrichmentPanel videoId={videoId} />,
    },
    {
      id: "transcript",
      label: t("video.tab.transcript"),
      content: <TranscriptTab videoId={videoId} />,
    },
    {
      id: "chapters",
      label: t("video.tab.chapters"),
      content: <ChaptersTab videoId={videoId} />,
    },
    {
      id: "files",
      label: t("video.tab.files"),
      content: <FilesTab videoId={videoId} />,
    },
    {
      id: "processing",
      label: t("video.tab.processing"),
      content: <ProcessingTab videoId={videoId} />,
    },
    {
      id: "stats",
      label: t("video.tab.stats"),
      content: <StatsTab videoId={videoId} />,
    },
  ];

  return (
    <section className="mkt-page mkt-video">
      <header className="mkt-page__header">
        <h1 dir="auto">{video.title || video.filename}</h1>
        <div className="mkt-video__flags">
          {video.detected_language && <Badge>{video.detected_language}</Badge>}
          <Badge tone={video.state === "ready" ? "success" : "neutral"}>{video.state}</Badge>
        </div>
      </header>
      <Tabs label={video.title || video.filename} items={items} defaultValue="watch" />
    </section>
  );
}

function ChaptersTab({ videoId }: { videoId: string }) {
  const { t } = useI18n();
  const [rows, setRows] = useState<Chapter[] | null>(null);
  useEffect(() => {
    api
      .get<{ items: Chapter[] }>(`/api/videos/${encodeURIComponent(videoId)}/chapters`)
      .then((r) => setRows(r.items ?? []))
      .catch(() => setRows([]));
  }, [videoId]);
  if (!rows) return <p className="mkt-loading">{t("common.loading")}</p>;
  if (rows.length === 0) return <EmptyState title={t("common.empty")} />;
  return (
    <ol className="mkt-chapters">
      {rows.map((c) => (
        <li key={c.id}>
          <Link to={`/videos/${videoId}/watch?t=${Math.floor(c.start_sec)}`}>
            <span className="mkt-chapters__time">{TS(c.start_sec)}</span>
            <span dir="auto">{c.title}</span>
          </Link>
        </li>
      ))}
    </ol>
  );
}

function FilesTab({ videoId }: { videoId: string }) {
  const { t } = useI18n();
  const [subs, setSubs] = useState<Subtitle[] | null>(null);
  useEffect(() => {
    api
      .get<{ items: Subtitle[] }>(`/api/videos/${encodeURIComponent(videoId)}/subtitles`)
      .then((r) => setSubs(r.items ?? []))
      .catch(() => setSubs([]));
  }, [videoId]);
  if (!subs) return <p className="mkt-loading">{t("common.loading")}</p>;
  return (
    <div>
      <h2>{t("video.subtitles")}</h2>
      {subs.length === 0 ? (
        <EmptyState title={t("common.empty")} />
      ) : (
        <ul className="mkt-list">
          {subs.map((s) => (
            <li key={s.id}>
              <a href={s.url}>
                {s.language} · {s.format}
              </a>{" "}
              {s.is_default && <Badge tone="accent">default</Badge>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

const TRANSCRIPT_WINDOW = 400;

function TranscriptTab({ videoId }: { videoId: string }) {
  const { t } = useI18n();
  const [segs, setSegs] = useState<Segment[] | null>(null);
  useEffect(() => {
    api
      .get<{ items: Segment[] }>(`/api/videos/${encodeURIComponent(videoId)}/segments`)
      .then((r) => setSegs(r.items ?? []))
      .catch(() => setSegs([]));
  }, [videoId]);
  if (!segs) return <p className="mkt-loading">{t("common.loading")}</p>;
  if (segs.length === 0) return <EmptyState title={t("video.transcript.empty")} />;
  // Window cap: render at most TRANSCRIPT_WINDOW rows to bound DOM size
  // on long transcripts (true virtualization deferred — see report).
  const shown = segs.slice(0, TRANSCRIPT_WINDOW);
  return (
    <div className="mkt-transcript">
      {shown.map((s) => (
        <p key={s.id} className="mkt-transcript__seg" dir="auto">
          <Link
            className="mkt-transcript__time"
            to={`/videos/${videoId}/watch?t=${Math.floor(s.start_sec)}`}
          >
            {TS(s.start_sec)}
          </Link>
          {s.speaker && <strong className="mkt-transcript__spk"> {s.speaker}: </strong>}
          <span>{s.text}</span>
        </p>
      ))}
      {segs.length > TRANSCRIPT_WINDOW && <p className="mkt-muted">{t("common.loadMore")}</p>}
    </div>
  );
}

function ProcessingTab({ videoId }: { videoId: string }) {
  const { t, n } = useI18n();
  const [jobs, setJobs] = useState<Job[] | null>(null);

  useEffect(() => {
    let alive = true;
    api
      .get<{ items: Job[] }>("/api/jobs?limit=200")
      .then((r) => {
        if (alive) setJobs((r.items ?? []).filter((j) => j.video_id === videoId));
      })
      .catch(() => {
        if (alive) setJobs([]);
      });
    // Live per-library updates (AC-3 {type,at,payload}).
    const off = subscribe(`library/${videoId}`, (ev) => {
      const job = ev.payload?.job as Job | undefined;
      if (!job || job.video_id !== videoId) return;
      setJobs((prev) => {
        if (!prev) return prev;
        const idx = prev.findIndex((j) => j.id === job.id);
        if (idx < 0) return [job, ...prev];
        const next = prev.slice();
        next[idx] = job;
        return next;
      });
    });
    return () => {
      alive = false;
      off();
    };
  }, [videoId]);

  if (!jobs) return <p className="mkt-loading">{t("common.loading")}</p>;
  if (jobs.length === 0) return <EmptyState title={t("common.empty")} />;

  return (
    <table className="mkt-table" aria-label={t("video.tab.processing")}>
      <thead>
        <tr>
          <th>{t("queue.col.stage")}</th>
          <th>{t("queue.col.state")}</th>
          <th>{t("queue.col.eta")}</th>
          <th>{t("queue.col.attempts")}</th>
        </tr>
      </thead>
      <tbody>
        {jobs.map((j) => (
          <tr key={j.id}>
            <td>{STAGE_LABEL[j.stage] ?? j.stage}</td>
            <td>
              <Badge tone={j.state === "failed" ? "error" : "neutral"}>{j.state}</Badge>
              {j.error && <span className="mkt-muted"> {j.error}</span>}
            </td>
            <td>
              {typeof j.estimated_remaining_sec === "number" ? TS(j.estimated_remaining_sec) : "—"}
            </td>
            <td>{n(j.attempts)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// StatsTab — Story 29.5 playback statistics. Everyone sees the aggregate
// cards; admins additionally get the per-viewer breakdown table (the API
// only returns `viewers` for admins, so the table is gated on its
// presence rather than re-checking the role here).
function StatsTab({ videoId }: { videoId: string }) {
  const { t } = useI18n();
  const { user } = useAuth();
  const [stats, setStats] = useState<VideoStats | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    analyticsApi
      .videoStats(videoId)
      .then((s) => alive && setStats(s))
      .catch((e) => alive && setErr(e instanceof ApiError ? e.message : String(e)));
    return () => {
      alive = false;
    };
  }, [videoId]);

  if (err) return <ErrorState kind="server" title={t("common.error")} description={err} />;
  if (!stats) return <p className="mkt-loading">{t("common.loading")}</p>;

  return (
    <div className="mkt-video__stats">
      <div className="mkt-analytics__kpis">
        <StatCard label={t("video.stats.views")} value={String(stats.total_views)} />
        <StatCard label={t("video.stats.uniqueViewers")} value={String(stats.unique_viewers)} />
        <StatCard
          label={t("video.stats.avgCompletion")}
          value={formatPercent(stats.avg_completion)}
        />
        <StatCard
          label={t("video.stats.avgWatch")}
          value={formatWatchTime(Math.round(stats.avg_watch_sec))}
        />
        <StatCard
          label={t("video.stats.completionRate")}
          value={formatPercentRatio(stats.completion_rate)}
        />
      </div>

      {user?.is_admin && stats.viewers && stats.viewers.length > 0 && (
        <details className="mkt-video__viewers">
          <summary>{t("video.stats.perUser")}</summary>
          <table className="mkt-table" aria-label={t("video.stats.perUser")}>
            <thead>
              <tr>
                <th>{t("analytics.col.user")}</th>
                <th>{t("video.stats.times")}</th>
                <th>{t("video.stats.totalWatch")}</th>
                <th>{t("video.stats.best")}</th>
              </tr>
            </thead>
            <tbody>
              {stats.viewers.map((v) => (
                <tr key={v.user_id}>
                  <td dir="auto">{v.username}</td>
                  <td>{v.times_watched}</td>
                  <td>{formatWatchTime(v.total_watch_sec)}</td>
                  <td>{formatPercent(v.best_percent)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="mkt-kpi mk-card mk-card--e1">
      <span className="mkt-kpi__value">{value}</span>
      <span className="mkt-kpi__label">{label}</span>
    </div>
  );
}
