// Four health tiles (NAS reads, CPU, Network, Backlog), each with a
// sparkline. The sparkline prefers the 4-hour series from
// GET /api/queues/health and falls back to the snapshot's short `spark`
// arrays (30 min) until that has loaded.
import { useI18n } from "../../lib/i18n";
import type { HealthNow, HealthSeries } from "../../lib/queueApi";
import { StatGrid, StatTile } from "./StatTile";
import { useFmt } from "./format";

const series = (all: HealthSeries[] | null, ...metrics: string[]): number[] | null => {
  for (const m of metrics) {
    const s = all?.find((x) => x.metric === m);
    if (s && s.points.length > 1) return s.points.map((p) => p.v);
  }
  return null;
};

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
  // null = not configured: only the probed services count
  const probed = [network.plex_ms, network.tv_ms, network.youtube_ms].filter(
    (v): v is number => v !== null
  );
  const reachable = probed.filter((v) => v >= 0).length;
  const hours = t("queue.health.window");
  const sparkOf = (values: number[], what: string) => ({
    values,
    label: t("queue.health.sparkAria", { what, window: hours }),
  });
  return (
    <StatGrid label={t("queue.health.title")} variant="health">
      <StatTile
        label={t("queue.health.nas")}
        value={n(nas.reads_busy)}
        unit={t("queue.health.slotsUnit", { limit: n(nas.reads_limit) })}
        tone={nas.timeouts_10m > 0 ? "warn" : undefined}
        spark={sparkOf(
          series(history, "nas.open_ms_p95", "nas.open_ms_p50") ?? spark.nas_open_ms,
          t("queue.health.nasOpen")
        )}
      >
        <p>
          {t("queue.health.open", { ms: fmt.ms(nas.open_ms_p50), p95: fmt.ms(nas.open_ms_p95) })}
        </p>
        <p className={nas.timeouts_10m > 0 ? "mkt-q-stat__warn" : undefined}>
          {t("queue.health.timeouts", { n: n(nas.timeouts_10m) })}
        </p>
      </StatTile>
      <StatTile
        label={t("queue.health.cpu")}
        value={cpu.pct === null ? "–" : n(Math.round(cpu.pct))}
        unit={cpu.pct === null ? undefined : "%"}
        spark={sparkOf(series(history, "cpu.pct") ?? spark.cpu_pct, t("queue.health.cpu"))}
      />
      <StatTile
        label={t("queue.health.network")}
        value={probed.length === 0 ? "–" : n(reachable)}
        unit={
          probed.length === 0
            ? undefined
            : t("queue.health.reachableUnit", { total: n(probed.length) })
        }
        tone={reachable < probed.length ? "error" : undefined}
        spark={sparkOf(series(history, "net.plex_ms") ?? [], "Plex")}
      >
        <ul className="mkt-q-stat__list">
          <li>
            <span>Plex</span> <span>{net(network.plex_ms)}</span>
          </li>
          <li>
            <span>dizqueTV</span> <span>{net(network.tv_ms)}</span>
          </li>
          <li>
            <span>YouTube</span> <span>{net(network.youtube_ms)}</span>
          </li>
        </ul>
      </StatTile>
      <StatTile
        label={t("queue.health.backlog")}
        value={n(backlog.ready)}
        unit={t("queue.health.waitingUnit")}
        spark={sparkOf(
          series(history, "backlog.ready") ?? spark.backlog_ready,
          t("queue.health.backlog")
        )}
      >
        <p>{t("queue.health.oldest", { time: fmt.dur(backlog.oldest_waiting_sec) })}</p>
      </StatTile>
    </StatGrid>
  );
}
