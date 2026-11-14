// Story 54.44: what a plugin uses, the same for every plugin. The memory is its process's
// as the machine's supervisor measured it. A runner group, the pipeline worker and the
// plugin host each run several plugins: the page says so and shows the whole process's
// number with the plugins that share it, never a share made up for one of them. CPU is
// not measured per process yet, and the page says that too.
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { Plugin, PluginDetail, ProcessUse } from "../../lib/pluginsApi";
import { joinNames } from "./componentWords";
import { jobTime, type T } from "./pluginWords";

/** The group's name in words (plugins.group.<group>), the process name when it has none. */
function groupName(t: T, u: ProcessUse): string {
  const key = `plugins.group.${u.group}`;
  const hit = t(key);
  return hit === key ? u.process : hit;
}

/** The others a process runs, in words: plugin names, and Maktaba itself. */
function sharers(t: T, locale: "en" | "ar", u: ProcessUse, names: (id: string) => string): string {
  const list = (u.shared_with ?? []).map((id) => bidiIsolate(names(id)));
  if (u.shared_core) list.push(t("plugins.resources.maktaba_itself"));
  return joinNames(locale, list);
}

/** One line for a card: its process's memory (said shared when it is) and its jobs now. */
export function ResourcesGlance({ plugin: p }: { plugin: Plugin }) {
  const { t, n } = useI18n();
  const procs = p.resources?.processes ?? [];
  const home = procs.filter((u) => u.machine === procs[0]?.machine);
  const measured = home.filter((u) => u.rss_mb !== undefined);
  const mb = measured.reduce((s, u) => s + (u.rss_mb ?? 0), 0);
  const memory = !measured.length
    ? t("plugins.resources.glance_none")
    : p.resources?.shared
      ? t("plugins.resources.glance_shared", { mb: n(mb) })
      : t("plugins.resources.glance", { mb: n(mb) });
  return (
    <p className="mkt-plugins__counts" data-testid="resources-glance">
      {memory}
      {" · "}
      {t("plugins.resources.jobs_now", {
        running: n(p.activity?.running ?? 0),
        pending: n(p.activity?.pending ?? 0),
      })}
    </p>
  );
}

export function ResourcesSection({
  plugin: p,
  names,
}: {
  plugin: PluginDetail;
  names: (id: string) => string;
}) {
  const { t, locale, n } = useI18n();
  const res = p.resources;
  const procs = res?.processes ?? [];
  const act = p.activity;
  const quota = res?.data_mb ?? 0;
  const usedMB = Math.ceil((res?.data_bytes ?? 0) / (1 << 20));
  return (
    <section aria-labelledby="pl-resources" id="resources">
      <h3 id="pl-resources">{t("plugins.section.resources")}</h3>
      <dl className="mkt-plugins__facts">
        <dt>{t("plugins.resources.running")}</dt>
        <dd>{n(act?.running ?? 0)}</dd>
        <dt>{t("plugins.resources.pending")}</dt>
        <dd>{n(act?.pending ?? 0)}</dd>
        <dt>{t("plugins.resources.avg")}</dt>
        <dd>{jobTime(t, n, act?.avg_ms)}</dd>
        <dt>{t("plugins.resources.p95")}</dt>
        <dd>{jobTime(t, n, act?.p95_ms)}</dd>
        {res?.memory_mb ? (
          <>
            <dt>{t("plugins.resources.memory_limit")}</dt>
            <dd>
              {res.peak_memory_mb !== undefined
                ? t("plugins.resources.memory_peak", {
                    peak: n(res.peak_memory_mb),
                    limit: n(res.memory_mb),
                  })
                : t("plugins.resources.mb", { mb: n(res.memory_mb) })}
            </dd>
          </>
        ) : null}
      </dl>
      {procs.length === 0 ? (
        <p className="mkt-plugins__hint">{t("plugins.resources.no_process")}</p>
      ) : (
        <table className="mkt-plugins__stages" data-testid="resources-processes">
          <thead>
            <tr>
              <th>{t("plugins.resources.process")}</th>
              <th>{t("plugins.resources.machine")}</th>
              <th>{t("plugins.resources.memory")}</th>
              <th>{t("plugins.resources.shared_with")}</th>
            </tr>
          </thead>
          <tbody>
            {procs.map((u) => {
              const others = sharers(t, locale, u, names);
              return (
                <tr key={u.machine + u.process}>
                  <td>
                    {groupName(t, u)} <code dir="ltr">{u.process}</code>
                  </td>
                  <td>{u.machine ? bidiIsolate(u.machine) : t("plugins.resources.no_machine")}</td>
                  <td>
                    {u.rss_mb === undefined
                      ? t("plugins.resources.not_measured")
                      : u.scope === "component"
                        ? t("plugins.resources.mb_component", { mb: n(u.rss_mb) })
                        : t("plugins.resources.mb", { mb: n(u.rss_mb) })}
                  </td>
                  <td>{others || t("plugins.resources.not_shared")}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {res?.shared && <p data-testid="resources-shared">{t("plugins.resources.shared_note")}</p>}
      {quota > 0 && (
        <div data-testid="resources-quota">
          <p>
            {t("plugins.health.data", {
              rows: n(res?.data_rows ?? 0),
              mb: n(usedMB),
              quota: n(quota),
            })}
          </p>
          <ProgressBar
            value={Math.min(100, Math.round((usedMB / quota) * 100))}
            label={t("plugins.resources.quota_label")}
          />
        </div>
      )}
      <p className="mkt-plugins__hint">{t("plugins.resources.cpu_note")}</p>
    </section>
  );
}
