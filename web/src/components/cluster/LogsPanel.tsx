// The Logs tab: pick a machine or a process, read its log live or from the
// files; below, every machine folder on the share with its processes and
// the days on record, each day opening the viewer.
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import type { ClusterStream } from "../../lib/clusterStream";
import { groupByMachine } from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useClusterFmt } from "./format";
import { LogViewer, type LogTarget } from "./LogViewer";
import type { FilesState } from "./useClusterFiles";

interface Props {
  stream: ClusterStream;
  files: FilesState;
  target: LogTarget | null;
  onTarget: (t: LogTarget | null) => void;
}

interface Option {
  value: string;
  label: string;
  target: LogTarget;
}

export function LogsPanel({ stream, files, target, onTarget }: Props) {
  const { t } = useI18n();
  const f = useClusterFmt();
  const nodes = files.files?.nodes ?? [];

  // machines and processes, live and on record
  const options: Option[] = [];
  const seen = new Set<string>();
  for (const g of groupByMachine(stream.processes)) {
    if (!seen.has(`ip:${g.ip}`)) {
      seen.add(`ip:${g.ip}`);
      options.push({
        value: `ip:${g.ip}`,
        label: t("cluster.logs.machineOption", { name: g.name, ip: g.ip }),
        target: { ip: g.ip, label: g.name },
      });
    }
    for (const p of g.processes) {
      options.push({
        value: `s:${p.session}`,
        label: t("cluster.logs.processOption", { name: g.name, worker: p.worker_id }),
        target: {
          ip: p.ip,
          session: p.session,
          workerId: p.worker_id,
          label: `${g.name} · ${p.worker_id}`,
        },
      });
    }
  }
  for (const node of nodes) {
    if (seen.has(`ip:${node.ip}`)) continue;
    seen.add(`ip:${node.ip}`);
    const name = node.processes.find((p) => p.name)?.name ?? node.ip;
    options.push({
      value: `ip:${node.ip}`,
      label: t("cluster.logs.machineOption", { name, ip: node.ip }),
      target: { ip: node.ip, label: name },
    });
  }
  const current = target ? (target.session ? `s:${target.session}` : `ip:${target.ip}`) : "";
  const node = target ? (nodes.find((x) => x.ip === target.ip) ?? null) : null;
  const where = f.files(files.files?.files);

  return (
    <div className="mkt-mc-section">
      <Select
        label={t("cluster.logs.target")}
        value={current}
        options={[
          { value: "", label: t("cluster.logs.pickTarget") },
          ...options.map((o) => ({ value: o.value, label: o.label })),
          ...(target && !options.some((o) => o.value === current)
            ? [{ value: current, label: target.label }]
            : []),
        ]}
        onChange={(e) => {
          const v = e.currentTarget.value;
          onTarget(options.find((o) => o.value === v)?.target ?? null);
        }}
      />
      {target ? (
        <LogViewer stream={stream} target={target} node={node} />
      ) : (
        <p className="mkt-mc-muted">{t("cluster.logs.none")}</p>
      )}

      <section className="mkt-mc-section" aria-labelledby="mkt-cl-files-h">
        <div className="mkt-mc-row">
          <h2 id="mkt-cl-files-h" className="mkt-mc-section__title">
            {t("cluster.files.title")}
          </h2>
          <Button size="sm" variant="ghost" onClick={files.reload}>
            {t("cluster.files.refresh")}
          </Button>
        </div>
        {files.files?.files && (
          <p className="mkt-mc-muted">
            {where} <code dir="ltr">{files.files.files.dir}</code>
          </p>
        )}
        {files.error && !files.files ? (
          <p className="mkt-mc-error" role="alert">
            {t("cluster.files.error")}
          </p>
        ) : nodes.length === 0 ? (
          <p className="mkt-mc-muted">
            {files.files ? t("cluster.files.none") : t("common.loading")}
          </p>
        ) : (
          <ul className="mkt-mc-list">
            {nodes.map((nd) => {
              const name = nd.processes.find((p) => p.name)?.name ?? nd.ip;
              const days = [...(nd.dates.logs ?? [])].sort().reverse();
              return (
                <li key={nd.ip} className="mkt-mc-card">
                  <div className="mkt-mc-row">
                    <strong>{bidiIsolate(name)}</strong>
                    <span className="mkt-mc-muted" dir="ltr">
                      {nd.ip}
                    </span>
                  </div>
                  {nd.processes.length > 0 && (
                    <ul
                      className="mkt-mc-chips"
                      aria-label={t("cluster.files.processes", { name })}
                    >
                      {nd.processes.map((p) => (
                        <li key={p.worker_id} className="mkt-mc-chip">
                          {p.worker_id}
                          {p.role ? ` · ${p.role}` : ""}
                          {p.host ? ` · ${p.host}` : ""}
                        </li>
                      ))}
                    </ul>
                  )}
                  {days.length === 0 ? (
                    <p className="mkt-mc-muted">{t("cluster.files.noLogs")}</p>
                  ) : (
                    <ul className="mkt-cl-days" aria-label={t("cluster.files.days", { name })}>
                      {days.map((d) => (
                        <li key={d}>
                          <button
                            type="button"
                            className="mkt-cl-link"
                            aria-label={t("cluster.files.openDay", { name, date: d })}
                            onClick={() => onTarget({ ip: nd.ip, label: name, date: d })}
                          >
                            {d}
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
