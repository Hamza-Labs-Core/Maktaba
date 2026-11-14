// Epic 54 (stories 54.13, 54.44): Settings → Plugins (admins). One list of every plugin of
// the home: the installed ones and the ones Maktaba publishes, each marked with who
// publishes it and where it came from, with a filter. Each card has the plugin's name,
// version, state in words, its own switch, its jobs of the last day, its open problems
// (a link to the failures view filtered to its stages) and what it uses at a glance, and
// opens the plugin's page at /settings/plugins/<id> (PluginPage.tsx). How to install is on
// the page: from the store or from a file (54.10: an unreviewed file is confirmed by typing
// its name), with what unreviewed means. What must always run is part of Maktaba itself,
// not a plugin: one line says so. The policy at the bottom: Allow unreviewed plugins, asked
// with the admin's password.
//
// A plugin's own text (its name, summary, setting labels) is data: shown as text, never as
// HTML, isolated for bidi.
import { useCallback, useEffect, useState } from "react";
import { Link, Route, Routes, useNavigate, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Toggle } from "@ds/components/Choice/Toggle";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { useAuth } from "../../lib/auth";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import {
  pluginErrorCode,
  pluginWord,
  pluginsApi,
  type Plugin,
  type TrustStatus,
  type Policy,
  type PluginIsolation,
} from "../../lib/pluginsApi";
import { ApiError } from "../../lib/api";
import { joinNames, phaseTone, phaseWord } from "./componentWords";
import { Browse } from "./Browse";
import { StoreAccount } from "./StoreAccount";
import { PanelOrder } from "./PanelOrder";
import { PluginPage, PublisherBadges } from "./PluginPage";
import { ResourcesGlance } from "./PluginResources";
import { PluginSwitch } from "./PluginSwitch";
import { RunnersWarning } from "./RunnersWarning";
import {
  byMaktaba,
  failuresLink,
  pluginName,
  refusalWords,
  statusWords,
  unreviewed,
  withRelease,
} from "./pluginWords";
import "./plugins.css";

export { capabilityTitle, refusalWords, unreviewed } from "./pluginWords";
export { SettingsSection, WritesSection } from "./PluginPage";

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
  // the list at /settings/plugins, a plugin's page at /settings/plugins/<id>
  return (
    <Routes>
      <Route index element={<PluginsInner />} />
      <Route path=":id" element={<PluginPage />} />
    </Routes>
  );
}

/** The list's filter, kept in the address (?show=): every plugin, Maktaba's, others' from
 * the store, or the ones installed from a file. */
export const FILTERS = ["all", "maktaba", "others", "file"] as const;
export type Filter = (typeof FILTERS)[number];

export function matchesFilter(p: Plugin, f: Filter): boolean {
  switch (f) {
    case "maktaba":
      return byMaktaba(p);
    case "others":
      return !byMaktaba(p) && p.source !== "file";
    case "file":
      return !byMaktaba(p) && p.source === "file";
    default:
      return true;
  }
}

function PluginsInner() {
  const { t, locale } = useI18n();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [plugins, setPlugins] = useState<Plugin[] | null>(null);
  const [core, setCore] = useState<{ component: string; title_key: string }[]>([]);
  const [policy, setPolicy] = useState<Policy>({ allow_unreviewed: false });
  const [error, setError] = useState("");
  const [installing, setInstalling] = useState(false);
  // ?store=<id>: the store's listing page sent the admin here (#757): Browse opens on it
  const [storeId] = useState(() => params.get("store") ?? "");
  const [browsing, setBrowsing] = useState(storeId !== "");
  const shown = params.get("show");
  const filter: Filter = (FILTERS as readonly string[]).includes(shown ?? "")
    ? (shown as Filter)
    : "all";

  const reload = useCallback(async () => {
    try {
      const r = await pluginsApi.list();
      setPlugins(r.plugins ?? []);
      setCore(r.core ?? []);
      setPolicy(r.policy);
      setError("");
    } catch (e) {
      setError(refusalWords(t, e));
    }
  }, [t]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const open = (id: string) => navigate(`/settings/plugins/${encodeURIComponent(id)}`);
  const names = (id: string) => {
    const hit = plugins?.find((p) => p.id === id);
    return hit ? pluginName(hit, locale) : id;
  };

  if (browsing) {
    return (
      <div className="mkt-settings__panel mkt-plugins">
        <Button variant="link" onClick={() => setBrowsing(false)}>
          {t("plugins.page.back")}
        </Button>
        <Browse initial={storeId} onInstalled={open} />
      </div>
    );
  }
  const list = (plugins ?? []).filter((p) => matchesFilter(p, filter));
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
      <RunnersWarning />
      {error && <p role="alert">{error}</p>}
      {plugins === null && !error && <p>{t("plugins.page.loading")}</p>}
      {plugins !== null && (
        <div
          className="mkt-plugins__filter"
          role="group"
          aria-label={t("plugins.filter.label")}
          data-testid="plugins-filter"
        >
          {FILTERS.map((f) => (
            <Button
              key={f}
              size="sm"
              variant={f === filter ? "primary" : "secondary"}
              aria-pressed={f === filter}
              onClick={() =>
                setParams(
                  (prev) => {
                    const next = new URLSearchParams(prev);
                    if (f === "all") next.delete("show");
                    else next.set("show", f);
                    return next;
                  },
                  { replace: true }
                )
              }
            >
              {t(`plugins.filter.${f}`)} (
              {(plugins ?? []).filter((p) => matchesFilter(p, f)).length})
            </Button>
          ))}
        </div>
      )}
      {plugins?.length === 0 && (
        <div data-testid="plugins-empty">
          <p>{t("plugins.page.none")}</p>
          <p className="mkt-plugins__hint">{t("plugins.page.none_how")}</p>
        </div>
      )}
      {plugins !== null && plugins.length > 0 && list.length === 0 && (
        <p>{t("plugins.filter.none")}</p>
      )}
      <ul className="mkt-plugins__cards">
        {list.map((p) => (
          <li key={p.id}>
            <PluginCard plugin={p} names={names} onChanged={reload} />
          </li>
        ))}
      </ul>
      {core.length > 0 && (
        <p className="mkt-plugins__hint" data-testid="plugins-core">
          {t("plugins.core.line", {
            parts: joinNames(
              locale,
              core.map((c) => t(c.title_key))
            ),
          })}{" "}
          <Link to="/settings/components">{t("plugins.core.components_link")}</Link>
          {" · "}
          <Link to="/admin/health">{t("plugins.core.health_link")}</Link>
        </p>
      )}
      <HowToInstall onBrowse={() => setBrowsing(true)} onFile={() => setInstalling(true)} />
      <PolicySection policy={policy} onChange={setPolicy} />
      <IsolationSection />
      <PanelOrder />
      <StoreAccount />
      {installing && (
        <InstallDialog
          onClose={() => setInstalling(false)}
          onInstalled={(id) => {
            setInstalling(false);
            open(id);
          }}
        />
      )}
    </div>
  );
}

/** How a plugin gets onto this home, in plain steps, and what unreviewed means. */
function HowToInstall({ onBrowse, onFile }: { onBrowse: () => void; onFile: () => void }) {
  const { t } = useI18n();
  return (
    <section aria-labelledby="pl-how" className="mkt-plugins__how" id="install">
      <h3 id="pl-how">{t("plugins.how.title")}</h3>
      <ol>
        <li>
          {t("plugins.how.store")}{" "}
          <Button variant="link" onClick={onBrowse}>
            {t("plugins.how.store_do")}
          </Button>
        </li>
        <li>
          {t("plugins.how.file")}{" "}
          <Button variant="link" onClick={onFile}>
            {t("plugins.how.file_do")}
          </Button>
        </li>
        <li>{t("plugins.how.grant")}</li>
        <li>{t("plugins.how.switch")}</li>
      </ol>
      <p>{t("plugins.how.maktaba")}</p>
      <p>
        <strong>{t("plugins.badge.unreviewed")}:</strong> {t("plugins.how.unreviewed")}
      </p>
    </section>
  );
}

export function PluginCard({
  plugin: p,
  names,
  onChanged,
}: {
  plugin: Plugin;
  names: (id: string) => string;
  onChanged: () => void | Promise<void>;
}) {
  const { t, locale, n } = useI18n();
  const name = pluginWord(p.names?.name, locale) || p.id;
  const href = `/settings/plugins/${encodeURIComponent(p.id)}`;
  const problems = p.activity?.problems ?? 0;
  return (
    <article className="mkt-plugins__card" aria-label={name}>
      <div className="mkt-plugins__card-head">
        <h3 className="mkt-plugins__name" title={name}>
          <Link to={href}>{bidiIsolate(name)}</Link>
        </h3>
        <span className="mkt-plugins__version" dir="ltr">
          {p.version}
        </span>
      </div>
      <div className="mkt-plugins__badges">
        <PublisherBadges plugin={p} />
        {!withRelease(p) &&
          (p.review_level === "development" ? (
            <Badge tone="warn">{t("plugins.badge.development")}</Badge>
          ) : unreviewed(p.review_level) ? (
            <Badge tone="error">{t("plugins.badge.unreviewed")}</Badge>
          ) : (
            <Badge tone="success">
              {t(
                p.review_level === "automated"
                  ? "plugins.badge.automated"
                  : "plugins.badge.reviewed"
              )}
            </Badge>
          ))}
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
        {!withRelease(p) && (
          <span className="mkt-plugins__rating" title={t("plugins.rating.why")}>
            {t("plugins.rating.value", { n: n(p.rating) })}
          </span>
        )}
      </div>
      <p className="mkt-plugins__summary">{bidiIsolate(pluginWord(p.names?.summary, locale))}</p>
      {statusWords(t, locale, p) !== phaseWord(t, p.phase || "off") && (
        <p className="mkt-plugins__status">{statusWords(t, locale, p)}</p>
      )}
      <PluginSwitch plugin={p} names={names} onChanged={onChanged} />
      <p className="mkt-plugins__counts">
        {t("plugins.health.counts_day", {
          done: n(p.activity?.done_24h ?? p.health?.jobs_done ?? 0),
          failed: n(p.activity?.failed_24h ?? p.health?.jobs_failed ?? 0),
        })}
        {" · "}
        {problems === 0 ? (
          t("plugins.errors.none")
        ) : (
          <Link to={failuresLink(p.queues ?? [])} data-testid="card-problems">
            {problems === 1
              ? t("plugins.errors.one")
              : t("plugins.errors.count", { n: n(problems) })}
          </Link>
        )}
      </p>
      <ResourcesGlance plugin={p} />
      <div className="mkt-plugins__card-actions">
        <Link to={href} className="mkt-plugins__open">
          {t("plugins.action.open")}
        </Link>
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

/**
 * Epic 59 story 59.07: the home's plugin isolation level, and on the Container level the
 * switch Allow plugins with container-only isolation (turning it on asks for the admin's
 * password, as Allow unreviewed plugins does). While it is off, third-party plugins stay
 * off with the reason (isolation_held).
 */
export function IsolationSection() {
  const { t } = useI18n();
  const [iso, setIso] = useState<PluginIsolation | null>(null);
  const [asking, setAsking] = useState(false);
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    pluginsApi.isolation().then(setIso, () => setIso(null));
  }, []);
  if (!iso || !iso.level) return null;
  const set = async (on: boolean, pw?: string) => {
    try {
      setIso(await pluginsApi.setIsolation(on, pw));
      setAsking(false);
      setPassword("");
      setError("");
    } catch (e) {
      setError(refusalWords(t, e));
    }
  };
  const container = iso.level === "container";
  return (
    <section
      aria-labelledby="pl-isolation"
      className="mkt-plugins__policy"
      data-testid="plugin-isolation"
    >
      <h3 id="pl-isolation">{t("plugins.isolation.title")}</h3>
      <p>{t(container ? "plugins.isolation.container" : "plugins.isolation.full")}</p>
      {(container || iso.allow_container_isolation) && (
        <>
          <Toggle
            label={t("plugins.allow_container")}
            checked={iso.allow_container_isolation}
            onChange={(e) => (e.target.checked ? setAsking(true) : void set(false))}
          />
          <p className="mkt-plugins__hint">{t("plugins.allow_container.explain")}</p>
        </>
      )}
      {iso.held && <p role="status">{t("plugins.held_isolation")}</p>}
      {error && !asking && <p role="alert">{error}</p>}
      {asking && (
        <Modal
          open
          onClose={() => setAsking(false)}
          title={t("plugins.allow_container")}
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
          <p>{t("plugins.allow_container.explain")}</p>
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
