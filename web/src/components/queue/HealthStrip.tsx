// Four health tiles (NAS reads, CPU, Network, Backlog), each with a
// sparkline. The sparkline prefers the 4-hour series from
// GET /api/queues/health and falls back to the snapshot's short `spark`
// arrays (30 min) until that has loaded.
import { useI18n } from "../../lib/i18n";
import type { HealthNow, HealthSeries } from "../../lib/queueApi";
import { Sparkline } from "./Sparkline";
import { useFmt } from "./format";

const series = (all: HealthSeries[] | null, ...metrics: string[]): number[] | null => {
  for (const m of metrics) {
    const s = all?.find((x) => x.metric === m);
    if (s && s.points.length > 1) return s.points.map((p) => p.v);
  }
  return null;
};

function Tile({
  id,
  title,
  children,
  spark,
  sparkLabel,
  tone,
}: {
  id: string;
  title: string;
  children: React.ReactNode;
  spark: number[];
  sparkLabel: string;
  tone?: "warn" | "error";
}) {
  return (
    <section className={`mkt-q-tile${tone ? ` mkt-q-tile--${tone}` : ""}`} aria-labelledby={id}>
      <h3 className="mkt-q-tile__title" id={id}>
        {title}
      </h3>
      <div className="mkt-q-tile__body">{children}</div>
      <Sparkline values={spark} label={sparkLabel} />
    </section>
  );
}

export function HealthStrip({
  health,
  history,
}: {
  health: HealthNow;
  history: HealthSeries[] | null;
}) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const { nas, cpu, network, backlog, spark } = health;
  const net = (ms: number | null) =>
    ms === null ? "–" : ms < 0 ? t("queue.health.unreachable") : fmt.ms(ms);
  const netBad = [network.plex_ms, network.tv_ms, network.youtube_ms].some(
    (v) => v !== null && v < 0
  );
  const hours = t("queue.health.window");
  return (
    <div className="mkt-q-health" role="group" aria-label={t("queue.health.title")}>
      <Tile
        id="mkt-q-h-nas"
        title={t("queue.health.nas")}
        spark={series(history, "nas.open_ms_p95", "nas.open_ms_p50") ?? spark.nas_open_ms}
        sparkLabel={t("queue.health.sparkAria", { what: t("queue.health.nasOpen"), window: hours })}
        tone={nas.timeouts_10m > 0 ? "warn" : undefined}
      >
        <p className="mkt-q-tile__big">
          {t("queue.health.slots", { busy: n(nas.reads_busy), limit: n(nas.reads_limit) })}
        </p>
        <p className="mkt-q-tile__line">
          {t("queue.health.open", { ms: fmt.ms(nas.open_ms_p50), p95: fmt.ms(nas.open_ms_p95) })}
        </p>
        <p className="mkt-q-tile__line">
          {nas.timeouts_10m > 0 && <span aria-hidden="true">! </span>}
          {t("queue.health.timeouts", { n: n(nas.timeouts_10m) })}
        </p>
      </Tile>
      <Tile
        id="mkt-q-h-cpu"
        title={t("queue.health.cpu")}
        spark={series(history, "cpu.pct") ?? spark.cpu_pct}
        sparkLabel={t("queue.health.sparkAria", { what: t("queue.health.cpu"), window: hours })}
      >
        <p className="mkt-q-tile__big">{cpu.pct === null ? "–" : `${n(Math.round(cpu.pct))}%`}</p>
      </Tile>
      <Tile
        id="mkt-q-h-net"
        title={t("queue.health.network")}
        spark={series(history, "net.plex_ms") ?? []}
        sparkLabel={t("queue.health.sparkAria", { what: "Plex", window: hours })}
        tone={netBad ? "error" : undefined}
      >
        <ul className="mkt-q-tile__list">
          <li>
            <span>Plex</span> <strong>{net(network.plex_ms)}</strong>
          </li>
          <li>
            <span>dizqueTV</span> <strong>{net(network.tv_ms)}</strong>
          </li>
          <li>
            <span>YouTube</span> <strong>{net(network.youtube_ms)}</strong>
          </li>
        </ul>
      </Tile>
      <Tile
        id="mkt-q-h-backlog"
        title={t("queue.health.backlog")}
        spark={series(history, "backlog.ready") ?? spark.backlog_ready}
        sparkLabel={t("queue.health.sparkAria", { what: t("queue.health.backlog"), window: hours })}
      >
        <p className="mkt-q-tile__big">{t("queue.health.waiting", { n: n(backlog.ready) })}</p>
        <p className="mkt-q-tile__line">
          {t("queue.health.oldest", { time: fmt.dur(backlog.oldest_waiting_sec) })}
        </p>
      </Tile>
    </div>
  );
}
