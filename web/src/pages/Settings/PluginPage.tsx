// Story 54.44: a plugin's page, at its own address (/settings/plugins/<id>), the same for
// every plugin: overview (what it does, its version, who publishes it and where this home
// got it), the stages it implements with their queues, what it may do in Maktaba's words,
// its status on each machine, what it uses, its errors (failed jobs and logs), its flow,
// its settings, its versions, what it wrote, and its switches. A section that does not
// apply to a plugin is absent, not empty: a plugin that comes with the Maktaba release has
// no grants to tick, no writes to revert, and is updated and removed with Maktaba only
// (until the store delivers it: stories 54.45 to 54.51), which its page says in words.
//
// A plugin's own text (its name, summary, setting labels) is data: shown as text, never as
// HTML, isolated for bidi.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Toggle } from "@ds/components/Choice/Toggle";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { fetchLibrarySummary, type LibrarySummary } from "../../lib/browse";
import {
  pluginErrorCode,
  pluginWord,
  pluginsApi,
  type FlowFinding,
  type FlowGraph,
  type Grants,
  type Plugin,
  type PluginDetail,
  type PluginFlow,
  type PluginSettings,
  type PluginWrite,
  type SettingProperty,
} from "../../lib/pluginsApi";
import { ApiError } from "../../lib/api";
import { logsPageLink } from "../../lib/logIndex";
import { rulesApi } from "../../lib/rulesApi";
import { localizeSections } from "./RulesSection";
import { phaseTone, phaseWord, reasonText } from "./componentWords";
import { PluginFlowChart } from "./PluginFlowChart";
import { ResourcesSection } from "./PluginResources";
import { PluginSwitch } from "./PluginSwitch";
import {
  byMaktaba,
  capabilityTitle,
  failuresLink,
  jobTime,
  pluginName,
  refusalWords,
  stageTitle,
  statusWords,
  withRelease,
  type T,
} from "./pluginWords";

/** The publisher and source of a plugin, as badges (the same on a card and on the page). */
export function PublisherBadges({ plugin: p }: { plugin: Plugin }) {
  const { t } = useI18n();
  return (
    <>
      {byMaktaba(p) ? (
        <Badge tone="success">
          {t(
            p.publisher?.verified
              ? "plugins.publisher.maktaba_verified"
              : "plugins.publisher.maktaba"
          )}
        </Badge>
      ) : (
        <Badge tone="neutral">{t(`plugins.source.${p.source ?? "file"}`)}</Badge>
      )}
    </>
  );
}

/** Where a plugin's logs are on the Logs page: a Maktaba plugin's by its component, any
 * other's by its own service (plugin.<id>). */
export function pluginLogsLink(p: Plugin, level?: string): string {
  return byMaktaba(p)
    ? logsPageLink({ components: p.component, level })
    : logsPageLink({ services: p.component, level });
}

/** The page for the plugin the address names. */
export function PluginPage() {
  const { id = "" } = useParams();
  const { t, locale, n, formatDate } = useI18n();
  const navigate = useNavigate();
  const [d, setD] = useState<PluginDetail | null>(null);
  const [all, setAll] = useState<Plugin[]>([]);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [confirm, setConfirm] = useState<"remove" | "purge" | "clear" | null>(null);
  // the newest read wins: an answer for a plugin the page has left, or older than one
  // already shown, is dropped
  const asked = useRef(0);
  const reload = useCallback(async () => {
    const mine = ++asked.current;
    try {
      const got = await pluginsApi.show(id);
      if (mine !== asked.current) return;
      setD(got);
      setError("");
    } catch (e) {
      if (mine !== asked.current) return;
      setD(null);
      setError(refusalWords(t, e));
    }
  }, [id, t]);
  useEffect(() => {
    // another plugin's address: its page starts empty, never with the last one's
    setD(null);
    setError("");
    setNote("");
    setConfirm(null);
    void reload();
    return () => {
      asked.current++;
    };
  }, [reload]);
  // the other plugins' names, for what shares a process and what a switch touches
  useEffect(() => {
    void pluginsApi.list(true).then(
      (r) => setAll(r.plugins ?? []),
      () => setAll([])
    );
  }, []);
  const names = useCallback(
    (pid: string) => {
      const hit = pid === d?.id ? d : all.find((p) => p.id === pid);
      return hit ? pluginName(hit, locale) : pid;
    },
    [all, d, locale]
  );
  const act = async (f: () => Promise<unknown>, done = "") => {
    try {
      await f();
      setNote(done);
      setError("");
      await reload();
    } catch (e) {
      if (pluginErrorCode(e) === "plugin_data_newer") setConfirm("clear");
      setError(refusalWords(t, e, locale));
    }
  };
  const back = (
    <Link to="/settings/plugins" className="mkt-plugins__back">
      {t("plugins.page.back")}
    </Link>
  );
  if (!d) {
    return (
      <div className="mkt-settings__panel mkt-plugins">
        {back}
        {error ? <p role="alert">{error}</p> : <p>{t("plugins.page.loading")}</p>}
      </div>
    );
  }
  const name = pluginName(d, locale);
  const queued = d.stages.reduce((s, x) => s + x.pending, 0);
  const release = withRelease(d);
  const queues = d.stages.map((s) => s.queue);
  const problems = d.activity?.problems ?? 0;
  return (
    <div className="mkt-settings__panel mkt-plugins mkt-plugins__page">
      {back}
      <div className="mkt-plugins__head">
        <h2 title={name}>{bidiIsolate(name)}</h2>
        <PluginSwitch plugin={d} names={names} onChanged={reload} />
      </div>
      <div className="mkt-plugins__badges">
        <PublisherBadges plugin={d} />
        <Badge tone={phaseTone(d.phase || "off") === "success" ? "success" : "warn"}>
          {phaseWord(t, d.phase || "off")}
        </Badge>
        {statusWords(t, locale, d) !== phaseWord(t, d.phase || "off") && (
          <span>{statusWords(t, locale, d)}</span>
        )}
      </div>
      {error && <p role="alert">{error}</p>}
      {note && <p role="status">{note}</p>}

      {d.revocation && (
        <p role="alert" className="mkt-plugins__revocation">
          {t(`plugins.revocation.${d.revocation.action}`, {
            reason: revocationReason(t, d.revocation.reason_code),
          })}
        </p>
      )}
      <section aria-labelledby="pl-overview" id="overview">
        <h3 id="pl-overview">{t("plugins.section.overview")}</h3>
        <p>{bidiIsolate(pluginWord(d.names?.summary, locale))}</p>
        <dl className="mkt-plugins__facts">
          <dt>{t("plugins.fact.version")}</dt>
          <dd>
            <span dir="ltr">{d.version}</span>
          </dd>
          <dt>{t("plugins.fact.publisher")}</dt>
          <dd>
            {byMaktaba(d) ? t("plugins.publisher.maktaba_verified") : t("plugins.publisher.other")}
          </dd>
          <dt>{t("plugins.fact.source")}</dt>
          <dd data-testid="plugin-source">{t(`plugins.source.long.${d.source ?? "file"}`)}</dd>
          {!release && (
            <>
              <dt>{t("plugins.fact.review")}</dt>
              <dd>{t(`plugins.review.${d.review_level}`)}</dd>
              <dt>{t("plugins.fact.installed")}</dt>
              <dd>{formatDate(d.installed_at)}</dd>
              <dt>{t("plugins.fact.range")}</dt>
              <dd>
                <span dir="ltr">{String(d.manifest?.maktaba ?? "")}</span>
              </dd>
              <dt>{t("plugins.fact.rating")}</dt>
              <dd>{t("plugins.rating.value", { n: n(d.rating) })}</dd>
            </>
          )}
        </dl>
      </section>

      {!release &&
        (d.review_level === "automated" || d.review_level === "reviewed") &&
        !d.revoked && (
          <section aria-labelledby="pl-auto">
            <h3 id="pl-auto" className="mkt-visually-hidden">
              {t("plugins.auto_update.label")}
            </h3>
            <Toggle
              label={t("plugins.auto_update.label")}
              checked={!!d.auto_update}
              onChange={(e) => void act(() => pluginsApi.setAutoUpdate(d.id, e.target.checked))}
            />
            <p className="mkt-plugins__hint">{t("plugins.auto_update.explain")}</p>
          </section>
        )}

      <section aria-labelledby="pl-stages" id="stages">
        <h3 id="pl-stages">{t("plugins.section.stages")}</h3>
        <table className="mkt-plugins__stages" data-testid="plugin-stages">
          <thead>
            <tr>
              <th>{t("plugins.stage.name")}</th>
              <th>{t("plugins.stage.pending")}</th>
              <th>{t("plugins.stage.running")}</th>
              <th>{t("plugins.stage.done")}</th>
              <th>{t("plugins.stage.failed")}</th>
              <th>{t("plugins.stage.time")}</th>
              <th>{t("plugins.stage.runs_in")}</th>
            </tr>
          </thead>
          <tbody>
            {d.stages.map((s) => (
              <tr key={s.queue}>
                <td>
                  <Link to={`/queue/${encodeURIComponent(s.queue)}`}>
                    {release ? stageTitle(t, s.name, s.label) : <span dir="ltr">{s.name}</span>}
                  </Link>
                </td>
                <td>{n(s.pending)}</td>
                <td>{n(s.running)}</td>
                <td>{n(s.done_24h)}</td>
                <td>{n(s.failed_24h)}</td>
                <td>{jobTime(t, n, s.avg_ms)}</td>
                <td>{s.runs_in ? groupWord(t, s.runs_in) : ""}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      {release ? (
        <section aria-labelledby="pl-perms" id="permissions">
          <h3 id="pl-perms">{t("plugins.section.permissions")}</h3>
          <p className="mkt-plugins__hint">{t("plugins.permissions.with_release")}</p>
          <ul className="mkt-plugins__caps" data-testid="plugin-capabilities">
            {(d.capabilities ?? []).map((c) => (
              <li key={c.key + c.param}>
                {t(c.key)}
                {c.param && <> · {bidiIsolate(c.param)}</>}
              </li>
            ))}
          </ul>
        </section>
      ) : (
        <PermissionsSection detail={d} onSaved={reload} />
      )}

      <section aria-labelledby="pl-status" id="status">
        <h3 id="pl-status">{t("plugins.section.status")}</h3>
        <p>
          {t("plugins.health.counts_day", {
            done: n(d.activity?.done_24h ?? 0),
            failed: n(d.activity?.failed_24h ?? 0),
          })}
          {!release && (
            <>
              {" · "}
              {t("plugins.health.counts", {
                done: n(d.health.jobs_done),
                failed: n(d.health.jobs_failed),
              })}
            </>
          )}
          {d.health.last_error_code && (
            <>
              {" · "}
              {t("plugins.health.last_error", {
                code: d.health.last_error_code,
                at: d.health.last_error_at ? formatDate(d.health.last_error_at) : "",
              })}
            </>
          )}
        </p>
        {(d.machines ?? []).length > 0 && (
          <table className="mkt-plugins__stages" data-testid="plugin-machines">
            <thead>
              <tr>
                <th>{t("plugins.resources.machine")}</th>
                <th>{t("plugins.machine.state")}</th>
                <th>{t("plugins.machine.restarts")}</th>
              </tr>
            </thead>
            <tbody>
              {(d.machines ?? []).map((m) => (
                <tr key={m.machine}>
                  <td>{bidiIsolate(m.machine)}</td>
                  <td>
                    {phaseWord(t, m.phase || "off")}
                    {m.reason && (
                      <>
                        {" · "}
                        {reasonText(t, locale, m.reason, (x) => x, m.reason_params ?? undefined)}
                      </>
                    )}
                    {m.stale && <> · {t("plugins.machine.stale")}</>}
                  </td>
                  <td>{n(m.restarts_24h)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <ResourcesSection plugin={d} names={names} />

      <section aria-labelledby="pl-errors" id="errors">
        <h3 id="pl-errors">{t("plugins.section.errors")}</h3>
        <p data-testid="plugin-problems">
          {problems === 0
            ? t("plugins.errors.none")
            : problems === 1
              ? t("plugins.errors.one")
              : t("plugins.errors.count", { n: n(problems) })}
        </p>
        <ul className="mkt-plugins__links">
          <li>
            <Link to={failuresLink(queues)} data-testid="plugin-failures-link">
              {t("plugins.errors.failures")}
            </Link>
          </li>
          <li>
            <Link to={pluginLogsLink(d, "error")} data-testid="plugin-error-logs-link">
              {t("plugins.errors.logs")}
            </Link>
          </li>
          <li>
            <Link to={pluginLogsLink(d)}>{t("plugins.health.logs")}</Link>
          </li>
        </ul>
        {!release && (
          <>
            <Toggle
              label={t("plugins.report_logs.label")}
              checked={!!d.report_logs}
              onChange={(e) => {
                const on = e.target.checked;
                void act(
                  () => pluginsApi.setReportLogs(d.id, on),
                  t(on ? "plugins.report_logs.on" : "plugins.report_logs.off")
                );
              }}
            />
            <p className="mkt-plugins__hint">{t("plugins.report_logs.hint")}</p>
          </>
        )}
      </section>

      <FlowSection id={d.id} names={names} home={!release} />

      {release ? <SettingsLinks plugin={d} /> : <SettingsSection id={d.id} />}
      {!release && <WritesSection id={d.id} />}

      <section aria-labelledby="pl-versions" id="versions">
        <h3 id="pl-versions">{t("plugins.section.versions")}</h3>
        <p>{t("plugins.versions.current", { version: d.version })}</p>
        {release ? (
          <p data-testid="versions-with-release">
            {t("plugins.versions.with_release")}{" "}
            <Link to="/admin/updates">{t("plugins.versions.updates_link")}</Link>
          </p>
        ) : (
          <>
            {d.previous_version && (
              <Button
                variant="secondary"
                onClick={() =>
                  void act(
                    () => pluginsApi.rollback(d.id, false),
                    t("plugins.versions.rolled_back")
                  )
                }
              >
                {t("plugins.versions.rollback", { version: d.previous_version })}
              </Button>
            )}
            <UpdateFromFile id={d.id} onDone={reload} />
          </>
        )}
      </section>

      <section aria-labelledby="pl-danger" className="mkt-plugins__danger" id="switches">
        <h3 id="pl-danger">{t("plugins.section.danger")}</h3>
        <div className="mkt-plugins__actions">
          {d.held_by ? (
            <Button
              variant="secondary"
              disabled={!!d.revoked}
              onClick={() => void act(() => pluginsApi.start(d.id))}
            >
              {t("plugins.action.start")}
            </Button>
          ) : (
            <Button variant="secondary" onClick={() => void act(() => pluginsApi.stop(d.id))}>
              {t("plugins.action.stop")}
            </Button>
          )}
          {!release && (
            <>
              <Button variant="destructive" onClick={() => setConfirm("remove")}>
                {t("plugins.action.remove")}
              </Button>
              <Button variant="destructive" onClick={() => setConfirm("purge")}>
                {t("plugins.action.purge")}
              </Button>
            </>
          )}
        </div>
        <p className="mkt-plugins__hint">{t("plugins.switch.stop_hint")}</p>
        {release && (
          <p data-testid="remove-with-release">
            {t("plugins.remove.with_release")}{" "}
            <Link to={`/settings/components#${d.component}`}>
              {t("plugins.switch.component_link")}
            </Link>
          </p>
        )}
      </section>

      {confirm && (
        <Modal
          open
          onClose={() => setConfirm(null)}
          title={
            confirm === "clear"
              ? t("plugins.versions.clear_title")
              : t("plugins.remove.title", { name: bidiIsolate(name) })
          }
          footer={
            <>
              <Button variant="ghost" onClick={() => setConfirm(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  const kind = confirm;
                  setConfirm(null);
                  if (kind === "clear")
                    void act(
                      () => pluginsApi.rollback(d.id, true),
                      t("plugins.versions.rolled_back")
                    );
                  else
                    void pluginsApi.remove(d.id, kind === "purge").then(
                      () => navigate("/settings/plugins"),
                      (e) => setError(refusalWords(t, e, locale))
                    );
                }}
              >
                {confirm === "clear"
                  ? t("plugins.versions.clear_confirm")
                  : t("plugins.action.remove")}
              </Button>
            </>
          }
        >
          {confirm === "clear" ? (
            <p>{t("plugins.versions.clear_body")}</p>
          ) : (
            <>
              <p>{t("plugins.remove.queued", { n: n(queued) })}</p>
              {confirm === "purge" ? (
                <p>{t("plugins.remove.purge_body", { n: n(d.writes) })}</p>
              ) : (
                <p>{t("plugins.remove.keep_body")}</p>
              )}
            </>
          )}
        </Modal>
      )}
    </div>
  );
}

/** Where a stage runs, in words (plugins.group.<group>). */
function groupWord(t: T, group: string): string {
  const key = `plugins.group.${group}`;
  const hit = t(key);
  return hit === key ? group : hit;
}

/** Where the settings of a plugin that comes with the release live today: the Rules
 * sections its component owns, the Models tab, and the server's settings file. A plugin
 * with none has no Settings section. */
function SettingsLinks({ plugin: p }: { plugin: Plugin }) {
  const { t, locale } = useI18n();
  const all = p.settings ?? [];
  const rules = all.filter((s) => s.startsWith("rules.")).map((s) => s.slice("rules.".length));
  const toml = all.filter((s) => s.startsWith("toml:")).map((s) => s.slice("toml:".length));
  // each section by its name as the Rules tab shows it: Maktaba's word for it, else the
  // title the server gives the section (never the section's identifier)
  const [titles, setTitles] = useState<Record<string, string>>({});
  const wanted = rules.join(",");
  useEffect(() => {
    if (!wanted) return;
    let live = true;
    void rulesApi.list().then(
      (r) => {
        if (!live) return;
        const out: Record<string, string> = {};
        for (const sec of localizeSections(r.sections ?? [], locale)) out[sec.key] = sec.title;
        setTitles(out);
      },
      () => undefined
    );
    return () => {
      live = false;
    };
  }, [wanted, locale]);
  if (rules.length + toml.length === 0) return null;
  const title = (section: string) => {
    const key = `rules.${section}.title`;
    const hit = t(key);
    return hit !== key ? hit : titles[section] || t("plugins.settings.section_unnamed");
  };
  return (
    <section aria-labelledby="pl-settings" id="settings">
      <h3 id="pl-settings">{t("plugins.section.settings")}</h3>
      {rules.length > 0 && (
        <>
          <p>{t("plugins.settings.in_rules")}</p>
          <ul className="mkt-plugins__links" data-testid="plugin-settings-links">
            {rules.map((s) => (
              <li key={s}>
                <Link to={`/settings/rules#${s}`}>{title(s)}</Link>
              </li>
            ))}
          </ul>
        </>
      )}
      {toml.length > 0 && (
        <p>
          {t("plugins.settings.in_file")}{" "}
          {toml.map((k) => (
            <code key={k} dir="ltr">
              {k}{" "}
            </code>
          ))}
        </p>
      )}
      <p className="mkt-plugins__hint">
        <Link to="/settings/models">{t("plugins.settings.models_link")}</Link>
      </p>
    </section>
  );
}

/** This plugin's flow (the whole set it works with, the members not installed named) and
 * the whole home's plugin graph, this plugin highlighted (story 54.41 §2). */
function FlowSection({
  id,
  names,
  home: wantHome,
}: {
  id: string;
  names: (id: string) => string;
  /** also draw the home's graph of installed plugins (not for a plugin of the release) */
  home: boolean;
}) {
  const { t, locale } = useI18n();
  const [flow, setFlow] = useState<PluginFlow | null>(null);
  const [home, setHome] = useState<{ graph: FlowGraph; findings: FlowFinding[] | null } | null>(
    null
  );
  useEffect(() => {
    let live = true;
    void pluginsApi.flow(id).then(
      (f) => live && setFlow(f),
      () => undefined
    );
    if (wantHome)
      void pluginsApi.graph().then(
        (g) => live && setHome(g),
        () => undefined
      );
    return () => {
      live = false;
    };
  }, [id, wantHome]);
  if (!flow && !home) return null;
  if (!home && flow && flow.graph.nodes.length === 0) return null;
  const warnings = (home?.findings ?? []).filter((f) => f.plugin === id);
  return (
    <section aria-labelledby="pl-flow" id="flow">
      <h3 id="pl-flow">{t("plugins.section.flow")}</h3>
      {flow && flow.missing.length > 0 && (
        <p role="status">
          {t("plugins.flow.works_with_missing", {
            names: flow.missing.map((m) => bidiIsolate(`${m.id} ${m.version}`)).join(", "),
          })}
        </p>
      )}
      {warnings.map((f, i) => (
        <p key={i} role="status">
          {locale === "ar" ? f.message_ar || f.message : f.message}
        </p>
      ))}
      {flow && (
        <PluginFlowChart
          graph={flow.graph}
          highlight={id}
          names={names}
          title={t("plugins.flow.this_flow")}
        />
      )}
      {home && home.graph.nodes.length > 0 && (
        <>
          <h4>{t("plugins.flow.home_graph")}</h4>
          <PluginFlowChart
            graph={home.graph}
            highlight={id}
            names={names}
            title={t("plugins.flow.home_graph")}
          />
        </>
      )}
    </section>
  );
}

function PermissionsSection({
  detail: d,
  onSaved,
}: {
  detail: PluginDetail;
  onSaved: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [grants, setGrants] = useState<Grants>(d.grants);
  const [libraries, setLibraries] = useState<LibrarySummary[]>([]);
  const [error, setError] = useState("");
  useEffect(() => setGrants(d.grants), [d.grants]);
  useEffect(() => {
    void fetchLibrarySummary()
      .then(setLibraries)
      .catch(() => setLibraries([]));
  }, []);
  const toggle = (list: string[], v: string, on: boolean) =>
    on ? [...list, v] : list.filter((x) => x !== v);
  const changed = JSON.stringify(grants) !== JSON.stringify(d.grants);
  const save = async () => {
    try {
      await pluginsApi.grants(d.id, grants);
      setError("");
      await onSaved();
    } catch (e) {
      setError(refusalWords(t, e));
    }
  };
  const missing = d.required.filter((c) => !grants.capabilities.includes(c));
  return (
    <section aria-labelledby="pl-perms" id="permissions">
      <h3 id="pl-perms">{t("plugins.section.permissions")}</h3>
      {d.required.length + d.optional.length === 0 && <p>{t("plugins.permissions.none")}</p>}
      <ul className="mkt-plugins__caps">
        {[
          ...d.required.map((c) => [c, true] as const),
          ...d.optional.map((c) => [c, false] as const),
        ].map(([c, req]) => {
          const w = capabilityTitle(t, c);
          return (
            <li key={c}>
              <Checkbox
                label={
                  <>
                    <strong>{w.title}</strong>
                    {req && <> {t("plugins.permissions.required")}</>}
                    <br />
                    <span className="mkt-plugins__hint">{w.detail}</span>
                  </>
                }
                checked={grants.capabilities.includes(c)}
                onChange={(e) =>
                  setGrants({
                    ...grants,
                    capabilities: toggle(grants.capabilities, c, e.target.checked),
                  })
                }
              />
            </li>
          );
        })}
      </ul>
      {missing.length > 0 && (
        <p className="mkt-plugins__hint">{t("plugins.permissions.required_hint")}</p>
      )}
      <h4>{t("plugins.permissions.libraries")}</h4>
      {!grants.all_libraries && grants.libraries.length === 0 && (
        <p className="mkt-plugins__warn">{t("plugins.permissions.no_library")}</p>
      )}
      <Checkbox
        label={t("plugins.permissions.all_libraries")}
        checked={grants.all_libraries}
        onChange={(e) => setGrants({ ...grants, all_libraries: e.target.checked })}
      />
      {!grants.all_libraries && (
        <ul className="mkt-plugins__libs">
          {libraries.map((l) => (
            <li key={l.id}>
              <Checkbox
                label={bidiIsolate(l.name)}
                checked={grants.libraries.includes(l.id)}
                onChange={(e) =>
                  setGrants({
                    ...grants,
                    libraries: toggle(grants.libraries, l.id, e.target.checked),
                  })
                }
              />
            </li>
          ))}
        </ul>
      )}
      <Button disabled={!changed} onClick={() => void save()}>
        {t("plugins.permissions.save")}
      </Button>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

/** The plugin's settings, drawn from its schema with its own words (54.11). */
export function SettingsSection({ id }: { id: string }) {
  const { t, locale } = useI18n();
  const [s, setS] = useState<PluginSettings | null>(null);
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    void pluginsApi.settings(id).then((r) => {
      setS(r);
      setValues(r.values);
    });
  }, [id]);
  const word = useCallback(
    (key?: string) => (key ? s?.strings?.[locale]?.[key] || s?.strings?.en?.[key] || key : ""),
    [s, locale]
  );
  if (!s?.schema || s.schema.properties.length === 0) return null;
  const save = async (v: Record<string, unknown>) => {
    try {
      const r = await pluginsApi.saveSettings(id, v);
      setS(r);
      setValues(r.values);
      setError("");
      setSaved(true);
    } catch (e) {
      setSaved(false);
      setError(refusalWords(t, e));
    }
  };
  const defaults = Object.fromEntries(s.schema.properties.map((p) => [p.name, p.default]));
  return (
    <section aria-labelledby="pl-settings" id="settings">
      <h3 id="pl-settings">{t("plugins.section.settings")}</h3>
      {s.reset && s.reset.length > 0 && (
        <p className="mkt-plugins__warn">
          {t("plugins.settings.reset_by_update", {
            fields: s.reset
              .map((f) => word(s.schema?.properties.find((p) => p.name === f)?.title_key) || f)
              .join(", "),
          })}
        </p>
      )}
      {s.schema.properties.map((p) => (
        <SettingField
          key={p.name}
          p={p}
          label={word(p.title_key)}
          help={word(p.help_key)}
          value={values[p.name]}
          onChange={(v) => setValues({ ...values, [p.name]: v })}
        />
      ))}
      <div className="mkt-plugins__actions">
        <Button onClick={() => void save(values)}>{t("common.save")}</Button>
        <Button variant="secondary" onClick={() => void save(defaults)}>
          {t("plugins.settings.reset")}
        </Button>
      </div>
      {saved && <p role="status">{t("plugins.settings.saved")}</p>}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

function SettingField({
  p,
  label,
  help,
  value,
  onChange,
}: {
  p: SettingProperty;
  label: string;
  help: string;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const id = `pl-set-${p.name}`;
  if (p.type === "boolean") {
    return (
      <Toggle
        id={id}
        label={label}
        checked={value === true}
        onChange={(e) => onChange(e.target.checked)}
      />
    );
  }
  if (p.type === "array") {
    const list = Array.isArray(value) ? (value as string[]) : [];
    return (
      <fieldset>
        <legend>{label}</legend>
        {(p.items_enum ?? []).map((o) => (
          <Checkbox
            key={o}
            label={o}
            checked={list.includes(o)}
            onChange={(e) =>
              onChange(e.target.checked ? [...list, o] : list.filter((x) => x !== o))
            }
          />
        ))}
      </fieldset>
    );
  }
  if (p.enum) {
    return (
      <Select
        id={id}
        label={label}
        description={help || undefined}
        value={String(value ?? "")}
        options={p.enum.map((o) => ({ value: o, label: o }))}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  const numeric = p.type === "integer" || p.type === "number";
  return (
    <Input
      id={id}
      label={label}
      description={help || undefined}
      type={numeric ? "number" : "text"}
      min={p.minimum}
      max={p.maximum}
      maxLength={p.max_length}
      step={p.type === "integer" ? 1 : undefined}
      value={value === undefined || value === null ? "" : String(value)}
      onChange={(e) =>
        onChange(numeric ? (e.target.value === "" ? "" : Number(e.target.value)) : e.target.value)
      }
    />
  );
}

/** What it wrote: the plugin's writes, newest first, with Revert selected (54.08). */
export function WritesSection({ id }: { id: string }) {
  const { t, formatDate } = useI18n();
  const [writes, setWrites] = useState<PluginWrite[]>([]);
  const [selected, setSelected] = useState<number[]>([]);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    try {
      setWrites((await pluginsApi.writes(id)).writes ?? []);
    } catch (e) {
      setError(refusalWords(t, e));
    }
  }, [id, t]);
  useEffect(() => {
    void load();
  }, [load]);
  const revert = async () => {
    try {
      const r = await pluginsApi.revert(id, selected);
      setNote(t("plugins.writes.reverted", { n: r.reverted }));
      setSelected([]);
      await load();
    } catch (e) {
      setError(refusalWords(t, e));
    }
  };
  const open = useMemo(() => writes.filter((w) => !w.reverted_at), [writes]);
  return (
    <section aria-labelledby="pl-writes" id="writes">
      <h3 id="pl-writes">{t("plugins.section.writes")}</h3>
      {writes.length === 0 && <p>{t("plugins.writes.none")}</p>}
      {writes.length > 0 && (
        <table className="mkt-plugins__writes">
          <thead>
            <tr>
              <th aria-label={t("plugins.writes.select")} />
              <th>{t("plugins.writes.what")}</th>
              <th>{t("plugins.writes.value")}</th>
              <th>{t("plugins.writes.when")}</th>
            </tr>
          </thead>
          <tbody>
            {writes.map((w) => (
              <tr key={w.id}>
                <td>
                  <Checkbox
                    aria-label={t("plugins.writes.select")}
                    disabled={!!w.reverted_at}
                    checked={selected.includes(w.id)}
                    onChange={(e) =>
                      setSelected(
                        e.target.checked ? [...selected, w.id] : selected.filter((x) => x !== w.id)
                      )
                    }
                  />
                </td>
                <td>
                  {t(`plugins.writes.target.${w.target}`)}{" "}
                  {w.field && <code dir="ltr">{w.field}</code>}
                </td>
                <td>
                  {bidiIsolate(typeof w.after === "string" ? w.after : JSON.stringify(w.after))}
                </td>
                <td>{w.reverted_at ? t("plugins.writes.was_reverted") : formatDate(w.at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {open.length > 0 && (
        <Button variant="secondary" disabled={selected.length === 0} onClick={() => void revert()}>
          {t("plugins.writes.revert", { n: selected.length })}
        </Button>
      )}
      {note && <p role="status">{note}</p>}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

function UpdateFromFile({ id, onDone }: { id: string; onDone: () => Promise<void> }) {
  const { t } = useI18n();
  const [file, setFile] = useState<File | null>(null);
  const [typed, setTyped] = useState("");
  const [askName, setAskName] = useState("");
  const [error, setError] = useState("");
  const send = async () => {
    if (!file) return;
    try {
      await pluginsApi.update(id, {
        file,
        fileName: file.name,
        grants: [],
        libraries: [],
        allLibraries: false,
        enable: false,
        confirmName: typed,
      });
      setFile(null);
      setError("");
      await onDone();
    } catch (e) {
      if (pluginErrorCode(e) === "plugin_name_mismatch" && e instanceof ApiError) {
        setAskName(String((e.problem as unknown as { name?: string }).name ?? ""));
      } else setError(refusalWords(t, e));
    }
  };
  return (
    <div className="mkt-plugins__update">
      <label>
        {t("plugins.versions.update_file")}
        <input
          type="file"
          accept=".mkplugin"
          onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        />
      </label>
      {askName && (
        <Input
          label={t("plugins.install.type_name", { name: bidiIsolate(askName) })}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
        />
      )}
      <Button variant="secondary" disabled={!file} onClick={() => void send()}>
        {t("plugins.versions.update")}
      </Button>
      {error && <p role="alert">{error}</p>}
    </div>
  );
}

/** A revocation's reason in Maktaba's words (the code itself when it has none). */
function revocationReason(t: T, code: string): string {
  const key = `plugins.revocation.reason.${code}`;
  const w = t(key);
  return w === key ? code : w;
}
