// Epic 54 (story 54.13): Settings → Plugins (admins). The installed plugins as cards (name
// in the admin's language, version, badges, rating, phase and why), each opening its page:
// overview, permissions (Maktaba's own words for each capability, libraries none until
// ticked), status and health (54.09), settings drawn from the plugin's schema (54.11), what
// it wrote with Revert selected, versions (roll back, update from a file) and the switches
// (turn off/on, stop/start, remove, remove and clear what it wrote). Install from a file
// (54.10): an unreviewed file is confirmed by typing its name. The policy at the bottom:
// Allow unreviewed plugins, asked with the admin's password.
//
// A plugin's own text (its name, summary, setting labels) is data: shown as text, never as
// HTML, isolated for bidi.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Toggle } from "@ds/components/Choice/Toggle";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { useAuth } from "../../lib/auth";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { fetchLibrarySummary, type LibrarySummary } from "../../lib/browse";
import {
  pluginErrorCode,
  pluginWord,
  pluginsApi,
  type BuiltInPlugin,
  type Grants,
  type Plugin,
  type PluginDetail,
  type TrustStatus,
  type PluginSettings,
  type PluginWrite,
  type Policy,
  type SettingProperty,
} from "../../lib/pluginsApi";
import { ApiError } from "../../lib/api";
import { logsPageLink } from "../../lib/logIndex";
import { phaseTone, phaseWord, reasonText } from "./componentWords";
import { Browse } from "./Browse";
import { PluginFlowChart } from "./PluginFlowChart";
import type { FlowFinding, FlowGraph, PluginFlow } from "../../lib/pluginsApi";
import { StoreAccount } from "./StoreAccount";
import { PanelOrder } from "./PanelOrder";
import "./plugins.css";

type T = (key: string, vars?: Record<string, string | number>) => string;

/** The words of a refusal: plugins.error.<code>, else the server's detail. */
export function refusalWords(
  t: T,
  e: unknown,
  locale: "en" | "ar" = typeof document !== "undefined" && document.documentElement.lang === "ar"
    ? "ar"
    : "en"
): string {
  // a flow check's refusal carries its own words, the cycle path named (54.39)
  if (e instanceof ApiError) {
    const p = e.problem as unknown as { message?: string; message_ar?: string } | undefined;
    const words = locale === "ar" ? p?.message_ar || p?.message : p?.message;
    if (words) return words;
  }
  const code = pluginErrorCode(e);
  if (code) {
    const key = `plugins.error.${code}`;
    const hit = t(key);
    if (hit !== key) return hit;
  }
  if (e instanceof ApiError) return e.problem?.detail || e.message;
  return e instanceof Error ? e.message : String(e);
}

/** A capability's title in Maktaba's words (capabilities.toml's title_key). */
export function capabilityTitle(t: T, cap: string): { title: string; detail: string } {
  const base = cap.split(":", 1)[0].replace(/[.-]/g, "_");
  const title = t(`plugins.capability.${base}.title`);
  const detail = t(`plugins.capability.${base}.detail`);
  const param = cap.includes(":") ? ` (${cap.slice(cap.indexOf(":") + 1)})` : "";
  return { title: title + param, detail };
}

// anything the store did not countersign (story 54.14 §2; "store" is Phase 1's name)
export const unreviewed = (level: string) =>
  level !== "store" && level !== "automated" && level !== "reviewed";

export function PluginsSection() {
  const { user } = useAuth();
  const { t } = useI18n();
  if (!user?.is_admin) {
    return (
      <ErrorState
        kind="permission"
        title={t("common.forbidden.title")}
        description={t("common.forbidden.desc")}
      />
    );
  }
  return <PluginsInner />;
}

function PluginsInner() {
  const { t } = useI18n();
  const [plugins, setPlugins] = useState<Plugin[] | null>(null);
  const [builtIn, setBuiltIn] = useState<BuiltInPlugin[]>([]);
  const [policy, setPolicy] = useState<Policy>({ allow_unreviewed: false });
  const [error, setError] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [installing, setInstalling] = useState(false);
  const [browsing, setBrowsing] = useState(false);

  const reload = useCallback(async () => {
    try {
      const r = await pluginsApi.list();
      setPlugins(r.plugins ?? []);
      setBuiltIn(r.built_in ?? []);
      setPolicy(r.policy);
      setError("");
    } catch (e) {
      setError(refusalWords(t, e));
    }
  }, [t]);

  useEffect(() => {
    void reload();
  }, [reload]);

  if (browsing && !open) {
    return (
      <div className="mkt-settings__panel mkt-plugins">
        <Button variant="link" onClick={() => setBrowsing(false)}>
          {t("plugins.page.back")}
        </Button>
        <Browse
          onInstalled={(id) => {
            setBrowsing(false);
            setOpen(id);
          }}
        />
      </div>
    );
  }
  if (open) {
    return (
      <PluginPage
        id={open}
        onBack={() => {
          setOpen(null);
          void reload();
        }}
      />
    );
  }
  return (
    <div className="mkt-settings__panel mkt-plugins">
      <div className="mkt-plugins__head">
        <h2>{t("plugins.page.title")}</h2>
        <div>
          <Button variant="ghost" onClick={() => setBrowsing(true)}>
            {t("plugins.browse.button")}
          </Button>
          <Button onClick={() => setInstalling(true)}>{t("plugins.install.button")}</Button>
        </div>
      </div>
      <p className="mkt-plugins__lead">{t("plugins.page.lead")}</p>
      <TrustBanner />
      {error && <p role="alert">{error}</p>}
      {plugins === null && !error && <p>{t("plugins.page.loading")}</p>}
      {plugins?.length === 0 && <p>{t("plugins.page.none")}</p>}
      <ul className="mkt-plugins__cards">
        {plugins?.map((p) => (
          <li key={p.id}>
            <PluginCard plugin={p} onOpen={() => setOpen(p.id)} />
          </li>
        ))}
      </ul>
      {builtIn.length > 0 && <BuiltInSection plugins={builtIn} />}
      <PolicySection policy={policy} onChange={setPolicy} />
      <PanelOrder />
      <StoreAccount />
      {installing && (
        <InstallDialog
          onClose={() => setInstalling(false)}
          onInstalled={(id) => {
            setInstalling(false);
            setOpen(id);
          }}
        />
      )}
    </div>
  );
}

/** "Built into Maktaba" (story 54.22 §4): Maktaba's own stages as first-party plugins, each
 * with what it can do in Maktaba's words. Its component is its switch (Components page). */
export function BuiltInSection({ plugins }: { plugins: BuiltInPlugin[] }) {
  const { t, locale } = useI18n();
  return (
    <section className="mkt-plugins__builtin" aria-labelledby="pl-builtin">
      <h3 id="pl-builtin">{t("plugins.builtin.title")}</h3>
      <p>{t("plugins.builtin.lead")}</p>
      <ul className="mkt-plugins__cards">
        {plugins.map((p) => {
          const name = (locale === "ar" ? p.name_ar : p.name) || p.name || p.id;
          const summary = (locale === "ar" ? p.summary_ar : p.summary) || p.summary;
          return (
            <li key={p.id}>
              <article className="mkt-plugins__card" aria-label={name}>
                <h4 className="mkt-plugins__name">{bidiIsolate(name)}</h4>
                {summary && <p>{summary}</p>}
                <details>
                  <summary>{t("plugins.builtin.can")}</summary>
                  <ul>
                    {p.capabilities.map((c) => (
                      <li key={c.key + c.param}>
                        {t(c.key)}
                        {c.param && <> · {bidiIsolate(c.param)}</>}
                      </li>
                    ))}
                  </ul>
                </details>
                <p className="mkt-plugins__switch">
                  {t("plugins.builtin.switch", { component: t(`components.${p.component}.title`) })}
                </p>
              </article>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/** The phase and its reason in words (Epic 51's), with a breaker's hold said as such. */
function statusWords(t: T, locale: "en" | "ar", p: Plugin): string {
  if (p.health?.tripped && p.held_by === "plugin-breaker")
    return t("components.reason.plugin_crash_loop");
  if (p.reason === "kept_off" || (p.kept_off && p.want === "installed"))
    return t("components.reason.kept_off");
  const why = reasonText(t, locale, p.reason, (id) => id, p.reason_params ?? undefined);
  return why || phaseWord(t, p.phase || "off");
}

export function PluginCard({ plugin: p, onOpen }: { plugin: Plugin; onOpen: () => void }) {
  const { t, locale, n } = useI18n();
  const name = pluginWord(p.names?.name, locale) || p.id;
  return (
    <article className="mkt-plugins__card" aria-label={name}>
      <div className="mkt-plugins__card-head">
        <h3 className="mkt-plugins__name" title={name}>
          {bidiIsolate(name)}
        </h3>
        <span className="mkt-plugins__version">{p.version}</span>
      </div>
      <div className="mkt-plugins__badges">
        {p.review_level === "development" ? (
          <Badge tone="warn">{t("plugins.badge.development")}</Badge>
        ) : unreviewed(p.review_level) ? (
          <Badge tone="error">{t("plugins.badge.unreviewed")}</Badge>
        ) : (
          <Badge tone="success">
            {t(
              p.review_level === "automated" ? "plugins.badge.automated" : "plugins.badge.reviewed"
            )}
          </Badge>
        )}
        {p.reason === "incompatible" && (
          <Badge tone="warn">{t("plugins.badge.incompatible")}</Badge>
        )}
        {p.revoked ? (
          <Badge tone="error">{t("plugins.badge.withdrawn")}</Badge>
        ) : (
          p.revocation?.action === "warn" && (
            <Badge tone="warn">{t("plugins.badge.store_warning")}</Badge>
          )
        )}
        <Badge tone={phaseTone(p.phase || "off") === "success" ? "success" : "neutral"}>
          {phaseWord(t, p.phase || "off")}
        </Badge>
        <span className="mkt-plugins__rating" title={t("plugins.rating.why")}>
          {t("plugins.rating.value", { n: n(p.rating) })}
        </span>
      </div>
      <p className="mkt-plugins__summary">{bidiIsolate(pluginWord(p.names?.summary, locale))}</p>
      <p className="mkt-plugins__status">{statusWords(t, locale, p)}</p>
      <p className="mkt-plugins__counts">
        {t("plugins.health.counts", {
          done: n(p.health?.jobs_done ?? 0),
          failed: n(p.health?.jobs_failed ?? 0),
        })}
      </p>
      <div className="mkt-plugins__card-actions">
        {p.held_by && (
          <Button
            size="sm"
            variant="secondary"
            onClick={() => void pluginsApi.start(p.id).then(onOpen)}
          >
            {t("plugins.action.start")}
          </Button>
        )}
        <Button size="sm" variant="secondary" onClick={onOpen}>
          {t("plugins.action.open")}
        </Button>
      </div>
    </article>
  );
}

// ---- install ---------------------------------------------------------------------------

function InstallDialog({
  onClose,
  onInstalled,
}: {
  onClose: () => void;
  onInstalled: (id: string) => void;
}) {
  const { t, locale } = useI18n();
  const [file, setFile] = useState<File | null>(null);
  const [enable, setEnable] = useState(false);
  const [expected, setExpected] = useState<{ en: string; ar: string } | null>(null);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState("");
  const send = async () => {
    if (!file) return;
    setBusy(true);
    setError("");
    try {
      const r = await pluginsApi.install(
        {
          file,
          fileName: file.name,
          grants: [],
          libraries: [],
          allLibraries: false,
          enable,
          confirmName: typed,
        },
        setProgress
      );
      onInstalled(r.result.plugin_id);
    } catch (e) {
      if (pluginErrorCode(e) === "plugin_name_mismatch" && e instanceof ApiError) {
        const p = e.problem as unknown as { name?: string; name_ar?: string };
        if (expected && typed) setError(t("plugins.install.wrong_name"));
        setExpected({ en: p.name ?? "", ar: p.name_ar ?? "" });
      } else {
        setError(refusalWords(t, e, locale));
      }
    } finally {
      setBusy(false);
    }
  };
  const name = expected ? (locale === "ar" && expected.ar ? expected.ar : expected.en) : "";
  return (
    <Modal
      open
      onClose={onClose}
      title={t("plugins.install.title")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            onClick={() => void send()}
            loading={busy}
            disabled={!file || (!!expected && !typed.trim())}
          >
            {enable ? t("plugins.install.and_enable") : t("plugins.install.install")}
          </Button>
        </>
      }
    >
      <p>{t("plugins.install.unreviewed_note")}</p>
      <label className="mkt-plugins__file">
        {t("plugins.install.file")}
        <input
          type="file"
          accept=".mkplugin"
          onChange={(e) => {
            setFile(e.target.files?.[0] ?? null);
            setExpected(null);
            setTyped("");
          }}
        />
      </label>
      <Checkbox
        label={t("plugins.install.enable")}
        checked={enable}
        onChange={(e) => setEnable(e.target.checked)}
      />
      <p className="mkt-plugins__hint">{t("plugins.install.permissions_after")}</p>
      {expected && (
        <Input
          label={t("plugins.install.type_name", { name: bidiIsolate(name) })}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          autoFocus
        />
      )}
      {busy && progress > 0 && progress < 1 && <progress value={progress} max={1} />}
      {error && <p role="alert">{error}</p>}
    </Modal>
  );
}

// ---- the plugin's page -----------------------------------------------------------------

function PluginPage({ id, onBack }: { id: string; onBack: () => void }) {
  const { t, locale, n, formatDate } = useI18n();
  const [d, setD] = useState<PluginDetail | null>(null);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [confirm, setConfirm] = useState<"remove" | "purge" | "clear" | null>(null);
  // the other plugins' stages that go idle when this one is off (54.39 §6)
  const [idle, setIdle] = useState<string[] | null>(null);
  const reload = useCallback(async () => {
    try {
      setD(await pluginsApi.show(id));
      setError("");
    } catch (e) {
      setError(refusalWords(t, e));
    }
  }, [id, t]);
  useEffect(() => {
    void reload();
  }, [reload]);
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
  if (!d) {
    return (
      <div className="mkt-settings__panel">
        <Button variant="link" onClick={onBack}>
          {t("plugins.page.back")}
        </Button>
        {error ? <p role="alert">{error}</p> : <p>{t("plugins.page.loading")}</p>}
      </div>
    );
  }
  const name = pluginWord(d.names?.name, locale) || d.id;
  const queued = d.stages.reduce((s, x) => s + x.pending, 0);
  return (
    <div className="mkt-settings__panel mkt-plugins mkt-plugins__page">
      <Button variant="link" onClick={onBack}>
        {t("plugins.page.back")}
      </Button>
      <h2 title={name}>{bidiIsolate(name)}</h2>
      {error && <p role="alert">{error}</p>}
      {note && <p role="status">{note}</p>}

      {d.revocation && (
        <p role="alert" className="mkt-plugins__revocation">
          {t(`plugins.revocation.${d.revocation.action}`, {
            reason: revocationReason(t, d.revocation.reason_code),
          })}
        </p>
      )}
      <section aria-labelledby="pl-overview">
        <h3 id="pl-overview">{t("plugins.section.overview")}</h3>
        <p>{bidiIsolate(pluginWord(d.names?.summary, locale))}</p>
        <dl className="mkt-plugins__facts">
          <dt>{t("plugins.fact.version")}</dt>
          <dd>{d.version}</dd>
          <dt>{t("plugins.fact.review")}</dt>
          <dd>{t(`plugins.review.${d.review_level}`)}</dd>
          <dt>{t("plugins.fact.installed")}</dt>
          <dd>{formatDate(d.installed_at)}</dd>
          <dt>{t("plugins.fact.range")}</dt>
          <dd dir="ltr">{String(d.manifest?.maktaba ?? "")}</dd>
          <dt>{t("plugins.fact.rating")}</dt>
          <dd>{t("plugins.rating.value", { n: n(d.rating) })}</dd>
        </dl>
      </section>

      {(d.review_level === "automated" || d.review_level === "reviewed") && !d.revoked && (
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

      <PermissionsSection detail={d} onSaved={reload} />

      <section aria-labelledby="pl-status">
        <h3 id="pl-status">{t("plugins.section.status")}</h3>
        <p>
          <Badge tone={phaseTone(d.phase || "off") === "success" ? "success" : "warn"}>
            {phaseWord(t, d.phase || "off")}
          </Badge>{" "}
          {statusWords(t, locale, d)}
        </p>
        <p>
          {t("plugins.health.counts", {
            done: n(d.health.jobs_done),
            failed: n(d.health.jobs_failed),
          })}
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
        <p>
          {t("plugins.health.data", {
            rows: n(d.data_rows),
            mb: n(Math.ceil(d.data_bytes / (1 << 20))),
            quota: n(d.data_mb),
          })}
        </p>
        <table className="mkt-plugins__stages">
          <thead>
            <tr>
              <th>{t("plugins.stage.name")}</th>
              <th>{t("plugins.stage.pending")}</th>
              <th>{t("plugins.stage.running")}</th>
              <th>{t("plugins.stage.done")}</th>
              <th>{t("plugins.stage.failed")}</th>
            </tr>
          </thead>
          <tbody>
            {d.stages.map((s) => (
              <tr key={s.queue}>
                <td dir="ltr">{s.name}</td>
                <td>{n(s.pending)}</td>
                <td>{n(s.running)}</td>
                <td>{n(s.done_24h)}</td>
                <td>{n(s.failed_24h)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <Link to={logsPageLink({ services: d.component })}>{t("plugins.health.logs")}</Link>
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
      </section>

      <FlowSection id={d.id} names={(pid) => (pid === d.id ? name : pid)} />

      <SettingsSection id={d.id} />
      <WritesSection id={d.id} />

      <section aria-labelledby="pl-versions">
        <h3 id="pl-versions">{t("plugins.section.versions")}</h3>
        <p>{t("plugins.versions.current", { version: d.version })}</p>
        {d.previous_version && (
          <Button
            variant="secondary"
            onClick={() =>
              void act(() => pluginsApi.rollback(d.id, false), t("plugins.versions.rolled_back"))
            }
          >
            {t("plugins.versions.rollback", { version: d.previous_version })}
          </Button>
        )}
        <UpdateFromFile id={d.id} onDone={reload} />
      </section>

      <section aria-labelledby="pl-danger" className="mkt-plugins__danger">
        <h3 id="pl-danger">{t("plugins.section.danger")}</h3>
        <div className="mkt-plugins__actions">
          {d.want === "enabled" ? (
            <Button
              variant="secondary"
              onClick={() =>
                void pluginsApi
                  .downstream(d.id)
                  .then((r) =>
                    r.stages.length ? setIdle(r.stages) : act(() => pluginsApi.disable(d.id))
                  )
                  .catch(() => act(() => pluginsApi.disable(d.id)))
              }
            >
              {t("plugins.action.disable")}
            </Button>
          ) : (
            <Button onClick={() => void act(() => pluginsApi.enable(d.id))}>
              {t("plugins.action.enable")}
            </Button>
          )}
          {d.held_by ? (
            <Button variant="secondary" onClick={() => void act(() => pluginsApi.start(d.id))}>
              {t("plugins.action.start")}
            </Button>
          ) : (
            <Button variant="secondary" onClick={() => void act(() => pluginsApi.stop(d.id))}>
              {t("plugins.action.stop")}
            </Button>
          )}
          <Button variant="destructive" onClick={() => setConfirm("remove")}>
            {t("plugins.action.remove")}
          </Button>
          <Button variant="destructive" onClick={() => setConfirm("purge")}>
            {t("plugins.action.purge")}
          </Button>
        </div>
      </section>

      {idle && (
        <Modal
          open
          onClose={() => setIdle(null)}
          title={t("plugins.flow.disable_title", { name: bidiIsolate(name) })}
          footer={
            <>
              <Button variant="ghost" onClick={() => setIdle(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  setIdle(null);
                  void act(() => pluginsApi.disable(d.id));
                }}
              >
                {t("plugins.action.disable")}
              </Button>
            </>
          }
        >
          <p>{t("plugins.flow.disable_body")}</p>
          <ul>
            {idle.map((s) => (
              <li key={s} dir="ltr">
                {s}
              </li>
            ))}
          </ul>
        </Modal>
      )}

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
                  else void act(() => pluginsApi.remove(d.id, kind === "purge").then(onBack));
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

/** This plugin's flow (the whole set it works with, the members not installed named) and
 * the whole home's plugin graph, this plugin highlighted (story 54.41 §2). */
function FlowSection({ id, names }: { id: string; names: (id: string) => string }) {
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
    void pluginsApi.graph().then(
      (g) => live && setHome(g),
      () => undefined
    );
    return () => {
      live = false;
    };
  }, [id]);
  if (!flow && !home) return null;
  const warnings = (home?.findings ?? []).filter((f) => f.plugin === id);
  return (
    <section aria-labelledby="pl-flow">
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
    <section aria-labelledby="pl-perms">
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
    <section aria-labelledby="pl-settings">
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
    <section aria-labelledby="pl-writes">
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

/**
 * When this home last checked the store's list of withdrawn plugins (54.14 §4, 54.17 §4):
 * an expired list warns (plugins keep running; store installs wait).
 */
function TrustBanner() {
  const { t, formatDate } = useI18n();
  const [trust, setTrust] = useState<TrustStatus | null>(null);
  useEffect(() => {
    pluginsApi.trust().then(setTrust, () => setTrust(null));
  }, []);
  if (!trust || !trust.store_keys) return null;
  if (!trust.revoked_issued) return <p className="mkt-plugins__hint">{t("plugins.trust.never")}</p>;
  const date = formatDate(trust.revoked_issued);
  if (trust.offline)
    return (
      <p role={trust.revoked_expired ? "alert" : undefined} className="mkt-plugins__hint">
        {t("plugins.trust.offline", { date })}
      </p>
    );
  return trust.revoked_expired ? (
    <p role="alert">{t("plugins.trust.expired", { date })}</p>
  ) : (
    <p className="mkt-plugins__hint">{t("plugins.trust.checked", { date })}</p>
  );
}

/** Allow unreviewed plugins: turning it on asks for the admin's password (54.10 §2). */
function PolicySection({ policy, onChange }: { policy: Policy; onChange: (p: Policy) => void }) {
  const { t } = useI18n();
  const [asking, setAsking] = useState(false);
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const set = async (on: boolean, pw?: string) => {
    try {
      onChange(await pluginsApi.setPolicy({ allow_unreviewed: on }, pw));
      setAsking(false);
      setPassword("");
      setError("");
    } catch (e) {
      setError(refusalWords(t, e));
    }
  };
  return (
    <section aria-labelledby="pl-policy" className="mkt-plugins__policy">
      <h3 id="pl-policy">{t("plugins.policy.title")}</h3>
      <Toggle
        label={t("plugins.policy.allow_unreviewed")}
        checked={policy.allow_unreviewed}
        onChange={(e) => (e.target.checked ? setAsking(true) : void set(false))}
      />
      <p className="mkt-plugins__hint">{t("plugins.policy.explain")}</p>
      {error && !asking && <p role="alert">{error}</p>}
      {asking && (
        <Modal
          open
          onClose={() => setAsking(false)}
          title={t("plugins.policy.confirm_title")}
          footer={
            <>
              <Button variant="ghost" onClick={() => setAsking(false)}>
                {t("common.cancel")}
              </Button>
              <Button disabled={!password} onClick={() => void set(true, password)}>
                {t("plugins.policy.confirm")}
              </Button>
            </>
          }
        >
          <p>{t("plugins.policy.explain")}</p>
          <Input
            type="password"
            label={t("plugins.policy.password")}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
          {error && <p role="alert">{error}</p>}
        </Modal>
      )}
    </section>
  );
}
