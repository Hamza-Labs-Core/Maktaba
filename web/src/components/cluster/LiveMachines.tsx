// "Running now": every machine connected to the hub (the home first), its
// CPU / memory / GPU / disk / network with 5-minute sparklines, and each of
// its processes — the jobs it is running, transcription speed, LLM load,
// the queues it pulls from, where it saves its files — with the commands.
import type { ReactNode } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { jobProgress } from "../../lib/clusterApi";
import {
  groupByMachine,
  series,
  type ClusterStream,
  type LiveProcess,
  type MachineGroup,
  type SeriesKey,
} from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { Sparkline } from "../queue/Sparkline";
import { useFmt } from "../queue/format";
import { useClusterFmt } from "./format";
import { ProcessActions } from "./ProcessActions";
import type { LogTarget } from "./LogViewer";

interface Props {
  stream: ClusterStream;
  queueLabel: (stage: string) => string;
  onOpenLogs: (target: LogTarget) => void;
}

export function LiveMachines({ stream, queueLabel, onOpenLogs }: Props) {
  const { t } = useI18n();
  const groups = groupByMachine(stream.processes);
  return (
    <section className="mkt-mc-section" aria-labelledby="mkt-cl-live-h">
      <h2 id="mkt-cl-live-h" className="mkt-mc-section__title">
        {t("cluster.live.title")}
      </h2>
      {groups.length === 0 ? (
        <p className="mkt-mc-muted">
          {stream.status === "unavailable" ? t("cluster.live.unavailable") : t("cluster.live.none")}
        </p>
      ) : (
        <ul className="mkt-mc-list">
          {groups.map((g) => (
            <MachineLive
              key={g.key}
              group={g}
              stream={stream}
              queueLabel={queueLabel}
              onOpenLogs={onOpenLogs}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

/** The process whose system metrics stand for the machine: online, most recently seen. */
function representative(g: MachineGroup): LiveProcess | undefined {
  return [...g.processes]
    .filter((p) => p.metrics?.system)
    .sort((a, b) => Number(b.online) - Number(a.online) || b.seenAt - a.seenAt)[0];
}

function Spark({
  stream,
  session,
  k,
  label,
}: {
  stream: ClusterStream;
  session: string | undefined;
  k: SeriesKey;
  label: string;
}) {
  const values = session ? series(stream.history[session], k) : [];
  if (values.length < 2) return null;
  return (
    <span className="mkt-cl-spark">
      <Sparkline values={values} label={label} width={96} height={24} />
    </span>
  );
}

function Tile({ label, value, children }: { label: string; value: string; children?: ReactNode }) {
  return (
    <div className="mkt-cl-tile">
      <dt>{label}</dt>
      <dd>
        <strong>{value}</strong>
        {children}
      </dd>
    </div>
  );
}

function MachineLive({ group, stream, queueLabel, onOpenLogs }: Props & { group: MachineGroup }) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const rep = representative(group);
  const m = rep?.metrics;
  const sys = m?.system;
  const online = group.processes.some((p) => p.online);
  const memUsed =
    sys?.mem_total !== undefined && sys?.mem_available !== undefined
      ? sys.mem_total - sys.mem_available
      : undefined;
  const memPct =
    memUsed !== undefined && sys?.mem_total ? (memUsed / sys.mem_total) * 100 : undefined;
  const nvidia = m?.gpu?.nvidia ?? [];
  const apple = m?.gpu?.apple;
  const name = bidiIsolate(group.name);
  const spark = (k: SeriesKey, what: string) => (
    <Spark
      stream={stream}
      session={rep?.session}
      k={k}
      label={t("cluster.live.sparkAria", { what, name: group.name })}
    />
  );

  return (
    <li className={`mkt-mc-card mkt-cl-machine${online ? "" : " mkt-cl-machine--off"}`}>
      <section aria-label={t("cluster.live.machineAria", { name: group.name })}>
        <header className="mkt-mc-row">
          <h3 className="mkt-mc-name">{name}</h3>
          <Badge tone={group.kind === "home" ? "accent" : "neutral"}>
            {t(group.kind === "home" ? "cluster.live.home" : "cluster.live.node")}
          </Badge>
          <Badge tone={online ? "success" : "neutral"}>
            {t(online ? "cluster.live.connected" : "cluster.live.disconnected")}
          </Badge>
          <span className="mkt-mc-muted" dir="ltr">
            {group.ip}
          </span>
          <button
            type="button"
            className="mkt-cl-link"
            onClick={() => onOpenLogs({ ip: group.ip, label: group.name })}
          >
            {t("cluster.logs.openMachine")}
          </button>
        </header>
        {sys || nvidia.length > 0 || apple ? (
          <dl className="mkt-cl-tiles">
            <Tile label={t("cluster.metric.cpu")} value={f.pct(sys?.cpu_pct)}>
              {sys?.load1 !== undefined && (
                <span className="mkt-mc-muted">
                  {t("cluster.metric.load", { n: f.num(sys.load1, 2) })}
                </span>
              )}
              {spark("cpu", t("cluster.metric.cpu"))}
            </Tile>
            <Tile label={t("cluster.metric.memory")} value={f.pct(memPct)}>
              <span className="mkt-mc-muted">
                {t("cluster.metric.ofTotal", {
                  used: f.bytes(memUsed),
                  total: f.bytes(sys?.mem_total),
                })}
              </span>
              {spark("mem", t("cluster.metric.memory"))}
            </Tile>
            {nvidia.length > 0 && (
              <Tile
                label={t("cluster.metric.gpu")}
                value={f.pct(Math.max(...nvidia.map((g) => g.util_pct ?? 0)))}
              >
                {nvidia.map((g, i) => (
                  <span key={i} className="mkt-mc-muted">
                    {t("cluster.metric.nvidia", {
                      name: g.name,
                      used: f.bytes(g.mem_used),
                      total: f.bytes(g.mem_total),
                      temp: n(g.temp_c),
                    })}
                  </span>
                ))}
                {spark("gpu", t("cluster.metric.gpu"))}
              </Tile>
            )}
            {nvidia.length === 0 && apple && (
              <Tile
                label={t("cluster.metric.appleGpu")}
                value={t("cluster.metric.gb", { n: f.num(apple.active_gb) })}
              >
                <span className="mkt-mc-muted">
                  {t("cluster.metric.appleDetail", {
                    cache: f.num(apple.cache_gb),
                    peak: f.num(apple.peak_gb),
                  })}
                </span>
                {spark("gpu", t("cluster.metric.appleGpu"))}
              </Tile>
            )}
            {(sys?.disks ?? []).map((d) => (
              <Tile
                key={d.path}
                label={t("cluster.metric.disk", { path: d.path })}
                value={t("cluster.metric.free", { free: f.bytes(d.free) })}
              >
                <span className="mkt-mc-muted">
                  {t("cluster.metric.ofDisk", { total: f.bytes(d.total) })}
                </span>
              </Tile>
            ))}
            {sys?.net && (
              <Tile
                label={t("cluster.metric.network")}
                value={t("cluster.metric.rxtx", {
                  rx: f.rate(sys.net.rx_bps),
                  tx: f.rate(sys.net.tx_bps),
                })}
              >
                {spark("rx", t("cluster.metric.rx"))}
                {spark("tx", t("cluster.metric.tx"))}
              </Tile>
            )}
          </dl>
        ) : (
          <p className="mkt-mc-muted">{t("cluster.live.noMetrics")}</p>
        )}
        <ul className="mkt-cl-procs" aria-label={t("cluster.live.processes", { name: group.name })}>
          {group.processes.map((p) => (
            <ProcessLive
              key={p.session}
              p={p}
              stream={stream}
              queueLabel={queueLabel}
              onOpenLogs={onOpenLogs}
            />
          ))}
        </ul>
      </section>
    </li>
  );
}

function ProcessLive({
  p,
  stream,
  queueLabel,
  onOpenLogs,
}: Omit<Props, "stream"> & { p: LiveProcess; stream: ClusterStream }) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const fmt = useFmt();
  const m = p.metrics;
  const pipe = m?.pipeline;
  const jobs = pipe?.jobs ?? [];
  const pulls = pipe?.stages ?? [];
  const notPulling = Object.entries(pipe?.stages_off ?? {});
  const files = f.files(m?.files);
  const title = `${p.worker_id}`;
  const spark = (k: SeriesKey, what: string) => (
    <Spark
      stream={stream}
      session={p.session}
      k={k}
      label={t("cluster.live.sparkAria", { what, name: p.worker_id })}
    />
  );
  return (
    <li className={`mkt-cl-proc${p.online ? "" : " mkt-cl-proc--off"}`}>
      <div className="mkt-mc-row">
        <strong dir="ltr">{title}</strong>
        <span className="mkt-mc-muted">
          {t("cluster.live.pid", { pid: n(p.pid, { useGrouping: false }) })}
          {pipe?.role && ` · ${pipe.role}`}
        </span>
        <Badge tone={p.online ? "success" : "neutral"}>
          {t(p.online ? "cluster.live.connected" : "cluster.live.disconnected")}
        </Badge>
        {p.online ? (
          <span className="mkt-mc-muted">
            {t("cluster.live.since", { time: fmt.since(p.connected_at) })}
          </span>
        ) : (
          p.disconnectedAt !== null && (
            <span className="mkt-mc-muted">
              {t("cluster.live.lostAt", { time: f.time(p.disconnectedAt) })}
            </span>
          )
        )}
      </div>
      {files && (
        <p className={`mkt-cl-files${m?.files?.on_share ? "" : " mkt-cl-files--local"}`}>{files}</p>
      )}
      <dl className="mkt-cl-tiles mkt-cl-tiles--small">
        {m?.process && (
          <Tile label={t("cluster.metric.process")} value={f.pct(m.process.cpu_pct)}>
            <span className="mkt-mc-muted">
              {t("cluster.metric.processDetail", {
                rss: f.bytes(m.process.rss),
                threads: n(m.process.threads ?? 0),
                up: fmt.dur(m.process.uptime_s),
              })}
            </span>
            {spark("pcpu", t("cluster.metric.process"))}
          </Tile>
        )}
        {pipe?.stt && (
          <Tile
            label={t("cluster.metric.stt")}
            value={t("cluster.metric.sttSpeed", { n: f.num(pipe.stt.audio_sec_per_s) })}
          >
            <span className="mkt-mc-muted">
              {t("cluster.metric.sttFiles", { n: n(pipe.stt.files_in_flight ?? 0) })}
            </span>
            {spark("stt", t("cluster.metric.stt"))}
          </Tile>
        )}
        {m?.llm && (
          <Tile
            label={t("cluster.metric.llm")}
            value={t("cluster.metric.tokens", { n: f.num(m.llm.tokens_per_s) })}
          >
            <span className="mkt-mc-muted">
              {t("cluster.metric.inFlight", { n: n(m.llm.in_flight ?? 0) })}
            </span>
            {spark("tok", t("cluster.metric.llm"))}
          </Tile>
        )}
        {pipe?.claims_per_min !== undefined && (
          <Tile label={t("cluster.metric.claims")} value={fmt.rate(pipe.claims_per_min)} />
        )}
      </dl>
      {jobs.length > 0 && (
        <ul className="mkt-cl-jobs" aria-label={t("cluster.live.jobsOf", { name: p.worker_id })}>
          {jobs.map((j) => {
            const pct = jobProgress(j.progress);
            return (
              <li key={String(j.job_id)}>
                <span>
                  <strong>{queueLabel(j.stage)}</strong> · {bidiIsolate(j.subject)}{" "}
                  <span className="mkt-mc-muted">
                    {t("queue.running.for", { time: fmt.since(j.since) })}
                  </span>
                </span>
                <ProgressBar
                  label={t("queue.running.progressAria", { label: j.subject })}
                  value={pct ?? undefined}
                  indeterminate={pct === null}
                />
              </li>
            );
          })}
        </ul>
      )}
      {(pulls.length > 0 || notPulling.length > 0) && (
        <div className="mkt-cl-pulls">
          {pulls.length > 0 && (
            <p>
              <span className="mkt-mc-muted">{t("cluster.live.pullsFrom")} </span>
              {pulls.map(queueLabel).join(t("cluster.list.sep"))}
            </p>
          )}
          {notPulling.length > 0 && (
            <ul className="mkt-cl-notpull" aria-label={t("cluster.live.notPulling")}>
              {notPulling.map(([s, r]) => (
                <li key={s}>
                  {t("cluster.live.notPullingOne", { queue: queueLabel(s), reason: f.reason(r) })}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      <ProcessActions
        process={p}
        onOpenLogs={() =>
          onOpenLogs({
            ip: p.ip,
            session: p.session,
            workerId: p.worker_id,
            label: `${p.node_name || p.host} · ${p.worker_id}`,
          })
        }
      />
    </li>
  );
}
