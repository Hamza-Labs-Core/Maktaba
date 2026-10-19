// "Running now": every machine connected to the hub (the home first, then
// by name), its CPU / memory / GPU / disk / network with 5-minute
// sparklines, and each of its processes with its commands.
//
// Built to hold still while the numbers move: every machine shows the same
// five tiles and every process the same four (a missing value reads "–"),
// each tile has a fixed size with one-line values, the sparkline's box is
// there before it has points, and the order never depends on a metric.
// The jobs a machine runs are a count with a link to the Processing page
// filtered by that machine; their progress bars live there.
import { memo, type ReactNode } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import {
  groupByMachine,
  series,
  type ClusterStream,
  type LiveProcess,
  type MachineGroup,
  type MetricSample,
  type SeriesKey,
} from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { Sparkline } from "../queue/Sparkline";
import { useFmt } from "../queue/format";
import { useClusterFmt } from "./format";
import { JobsSummary } from "./JobsSummary";
import { HOME_HOST } from "./machineSummary";
import { ProcessActions } from "./ProcessActions";
import type { LogTarget } from "./LogViewer";

interface Props {
  stream: ClusterStream;
  queueLabel: (stage: string) => string;
  onOpenLogs: (target: LogTarget) => void;
  /** jobs running per machine (node id / "home"); null while not known */
  jobs?: Record<string, number> | null;
}

export function LiveMachines({ stream, queueLabel, onOpenLogs, jobs = null }: Props) {
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
          {groups.map((g) => {
            const host = g.kind === "home" ? HOME_HOST : g.nodeId;
            return (
              <MachineLive
                key={g.key}
                group={g}
                history={stream.history}
                queueLabel={queueLabel}
                onOpenLogs={onOpenLogs}
                host={host}
                jobs={jobs && host ? (jobs[host] ?? 0) : null}
              />
            );
          })}
        </ul>
      )}
    </section>
  );
}

/**
 * The process whose system metrics stand for the machine: the first online
 * one with metrics in worker order (a stable pick, so the tiles do not jump
 * between processes as their frames arrive), else the first with metrics.
 */
export function representative(g: MachineGroup): LiveProcess | undefined {
  const withMetrics = g.processes.filter((p) => p.metrics?.system);
  return withMetrics.find((p) => p.online) ?? withMetrics[0];
}

/** A sparkline in a box that is there (empty) before the line has two points. */
function Spark({ values, label }: { values: number[]; label: string }) {
  return (
    <span className="mkt-cl-spark">
      {values.length >= 2 && <Sparkline values={values} label={label} width={96} height={24} />}
    </span>
  );
}

function Tile({
  label,
  value,
  detail,
  spark,
}: {
  label: string;
  value: string;
  detail?: ReactNode;
  spark?: ReactNode;
}) {
  return (
    <div className="mkt-cl-tile">
      <dt>{label}</dt>
      <dd>
        <strong className="mkt-cl-tile__value" title={value}>
          {value}
        </strong>
        <span className="mkt-cl-tile__detail">{detail ?? " "}</span>
        {spark}
      </dd>
    </div>
  );
}

const MachineLive = memo(function MachineLive({
  group,
  history,
  queueLabel,
  onOpenLogs,
  host,
  jobs,
}: {
  group: MachineGroup;
  history: Record<string, MetricSample[]>;
  queueLabel: (stage: string) => string;
  onOpenLogs: (target: LogTarget) => void;
  host: string | null;
  jobs: number | null;
}) {
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
  const disks = [...(sys?.disks ?? [])].sort((a, b) => a.path.localeCompare(b.path));
  const disk = disks[0];
  const name = bidiIsolate(group.name);
  const samples = rep ? history[rep.session] : undefined;
  const spark = (k: SeriesKey, what: string) => (
    <Spark
      values={series(samples, k)}
      label={t("cluster.live.sparkAria", { what, name: group.name })}
    />
  );

  const gpu =
    nvidia.length > 0
      ? {
          label: t("cluster.metric.gpu"),
          value: f.pct(Math.max(...nvidia.map((g) => g.util_pct ?? 0))),
          detail: nvidia
            .map((g) =>
              t("cluster.metric.nvidia", {
                name: g.name,
                used: f.bytes(g.mem_used),
                total: f.bytes(g.mem_total),
                temp: n(g.temp_c),
              })
            )
            .join(t("cluster.list.sep")),
        }
      : apple
        ? {
            label: t("cluster.metric.appleGpu"),
            value: t("cluster.metric.gb", { n: f.num(apple.active_gb) }),
            detail: t("cluster.metric.appleDetail", {
              cache: f.num(apple.cache_gb),
              peak: f.num(apple.peak_gb),
            }),
          }
        : { label: t("cluster.metric.gpu"), value: "–", detail: undefined };

  return (
    <li className={`mkt-mc-card mkt-cl-machine${online ? "" : " mkt-cl-machine--off"}`}>
      <section aria-label={t("cluster.live.machineAria", { name: group.name })}>
        <header className="mkt-mc-row mkt-mc-row--head">
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
          {host && <JobsSummary host={host} count={jobs} name={group.name} />}
        </header>
        <dl className="mkt-cl-tiles">
          <Tile
            label={t("cluster.metric.cpu")}
            value={f.pct(sys?.cpu_pct)}
            detail={
              sys?.load1 !== undefined ? t("cluster.metric.load", { n: f.num(sys.load1, 2) }) : ""
            }
            spark={spark("cpu", t("cluster.metric.cpu"))}
          />
          <Tile
            label={t("cluster.metric.memory")}
            value={f.pct(memPct)}
            detail={t("cluster.metric.ofTotal", {
              used: f.bytes(memUsed),
              total: f.bytes(sys?.mem_total),
            })}
            spark={spark("mem", t("cluster.metric.memory"))}
          />
          <Tile
            label={gpu.label}
            value={gpu.value}
            detail={gpu.detail}
            spark={spark("gpu", gpu.label)}
          />
          <Tile
            label={t("cluster.metric.disk", { path: disk?.path ?? "" }).trim()}
            value={disk ? t("cluster.metric.free", { free: f.bytes(disk.free) }) : "–"}
            detail={
              disk
                ? [
                    t("cluster.metric.ofDisk", { total: f.bytes(disk.total) }),
                    ...disks
                      .slice(1)
                      .map((d) =>
                        t("cluster.metric.diskMore", { path: d.path, free: f.bytes(d.free) })
                      ),
                  ].join(t("cluster.list.sep"))
                : undefined
            }
            spark={<span className="mkt-cl-spark" />}
          />
          <Tile
            label={t("cluster.metric.network")}
            value={
              sys?.net
                ? t("cluster.metric.rxtx", {
                    rx: f.rate(sys.net.rx_bps),
                    tx: f.rate(sys.net.tx_bps),
                  })
                : "–"
            }
            spark={spark("rx", t("cluster.metric.rx"))}
          />
        </dl>
        <ul className="mkt-cl-procs" aria-label={t("cluster.live.processes", { name: group.name })}>
          {group.processes.map((p) => (
            <ProcessLive
              key={p.session}
              p={p}
              samples={history[p.session]}
              queueLabel={queueLabel}
              onOpenLogs={onOpenLogs}
            />
          ))}
        </ul>
      </section>
    </li>
  );
});

const ProcessLive = memo(function ProcessLive({
  p,
  samples,
  queueLabel,
  onOpenLogs,
}: {
  p: LiveProcess;
  samples: MetricSample[] | undefined;
  queueLabel: (stage: string) => string;
  onOpenLogs: (target: LogTarget) => void;
}) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  const fmt = useFmt();
  const m = p.metrics;
  const pipe = m?.pipeline;
  const jobs = pipe?.jobs ?? [];
  const pulls = pipe?.stages ?? [];
  const notPulling = Object.entries(pipe?.stages_off ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const files = f.files(m?.files);
  const spark = (k: SeriesKey, what: string) => (
    <Spark
      values={series(samples, k)}
      label={t("cluster.live.sparkAria", { what, name: p.worker_id })}
    />
  );
  const pullsText = pulls.length ? pulls.map(queueLabel).join(t("cluster.list.sep")) : "–";
  const notPullingText = notPulling
    .map(([s, r]) => t("cluster.live.notPullingOne", { queue: queueLabel(s), reason: f.reason(r) }))
    .join("; ");
  return (
    <li className={`mkt-cl-proc${p.online ? "" : " mkt-cl-proc--off"}`}>
      <div className="mkt-mc-row mkt-mc-row--head">
        <strong dir="ltr">{p.worker_id}</strong>
        <span className="mkt-mc-muted">
          {t("cluster.live.pid", { pid: n(p.pid, { useGrouping: false }) })}
          {pipe?.role && ` · ${pipe.role}`}
        </span>
        <Badge tone={p.online ? "success" : "neutral"}>
          {t(p.online ? "cluster.live.connected" : "cluster.live.disconnected")}
        </Badge>
        <span className="mkt-mc-muted mkt-cl-proc__jobs">
          {t("machines.jobs.count", { n: p.online ? n(jobs.length) : "–" })}
        </span>
        <span className="mkt-mc-muted mkt-cl-proc__since">
          {p.online
            ? t("cluster.live.since", { time: fmt.since(p.connected_at) })
            : p.disconnectedAt !== null
              ? t("cluster.live.lostAt", { time: f.time(p.disconnectedAt) })
              : ""}
        </span>
      </div>
      <p
        className={`mkt-cl-files mkt-cl-line${m?.files?.on_share ? "" : " mkt-cl-files--local"}`}
        title={files ?? undefined}
      >
        {files ?? " "}
      </p>
      <dl className="mkt-cl-tiles mkt-cl-tiles--small">
        <Tile
          label={t("cluster.metric.process")}
          value={f.pct(m?.process?.cpu_pct)}
          detail={
            m?.process
              ? t("cluster.metric.processDetail", {
                  rss: f.bytes(m.process.rss),
                  threads: n(m.process.threads ?? 0),
                  up: fmt.dur(m.process.uptime_s),
                })
              : undefined
          }
          spark={spark("pcpu", t("cluster.metric.process"))}
        />
        <Tile
          label={t("cluster.metric.stt")}
          value={
            pipe?.stt ? t("cluster.metric.sttSpeed", { n: f.num(pipe.stt.audio_sec_per_s) }) : "–"
          }
          detail={
            pipe?.stt
              ? t("cluster.metric.sttFiles", { n: n(pipe.stt.files_in_flight ?? 0) })
              : undefined
          }
          spark={spark("stt", t("cluster.metric.stt"))}
        />
        <Tile
          label={t("cluster.metric.llm")}
          value={m?.llm ? t("cluster.metric.tokens", { n: f.num(m.llm.tokens_per_s) }) : "–"}
          detail={m?.llm ? t("cluster.metric.inFlight", { n: n(m.llm.in_flight ?? 0) }) : undefined}
          spark={spark("tok", t("cluster.metric.llm"))}
        />
        <Tile
          label={t("cluster.metric.claims")}
          value={pipe?.claims_per_min !== undefined ? fmt.rate(pipe.claims_per_min) : "–"}
          spark={<span className="mkt-cl-spark" />}
        />
      </dl>
      <div className="mkt-cl-pulls">
        <p className="mkt-cl-line" title={pullsText}>
          <span className="mkt-mc-muted">{t("cluster.live.pullsFrom")} </span>
          {pullsText}
        </p>
        <p className="mkt-cl-line mkt-cl-notpull" title={notPullingText || undefined}>
          {notPullingText || " "}
        </p>
      </div>
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
});
