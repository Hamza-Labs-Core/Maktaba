// Story 11.6 — Settings.
//
//   Account     — sign-out-everywhere (POST /api/auth/logout-all), a link
//                 to personal access tokens (/account/tokens, 11.13) and
//                 the diagnostics bundle
//   Sessions    — a link to the per-session list (/account/sessions, 11.14)
//   Appearance  — theme light/dark/system (11.8), language en/ar (11.12),
//                 density comfortable/compact (persisted)
//   Privacy     — tracking switch, recent history, activity timeline (29.4)
//   About       — app version and updates (Epic 28)
import { useEffect, useRef, useState } from "react";
import { Link, Navigate, NavLink, Route, Routes, useNavigate } from "react-router-dom";
import { RadioGroup, Radio } from "@ds/components/Choice/Radio";
import { Button } from "@ds/components/Button/Button";
import { useAuth } from "../lib/auth";
import { useI18n, type Locale } from "../lib/i18n";
import { readMode, setMode, type ThemeMode } from "../lib/theme";
import { ModelsSection } from "./Settings/ModelsSection";
import { RulesSection } from "./Settings/RulesSection";
import { UpdatesSettings } from "./Settings/UpdatesSettings";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { api, downloadBlob, ApiError } from "../lib/api";
import { analyticsApi, formatPercent, formatWatchTime, type HistoryItem } from "../lib/analytics";
import { ActivityTimeline } from "./Profile/WatchHistory";
import {
  fetchUpdateStatus,
  updateAvailable,
  useUpdateStatus,
  type UpdateStatus,
} from "../lib/updates";
import { Markdown } from "../lib/markdown";

const DENSITY_KEY = "mkt:density";
type Density = "comfortable" | "compact";

function readDensity(): Density {
  try {
    const d = localStorage.getItem(DENSITY_KEY);
    if (d === "comfortable" || d === "compact") return d;
  } catch {
    /* ignored */
  }
  return "comfortable";
}

function applyDensity(d: Density) {
  document.documentElement.dataset.density = d;
  try {
    localStorage.setItem(DENSITY_KEY, d);
  } catch {
    /* ignored */
  }
}

export function Settings() {
  const { t } = useI18n();
  const hasUpdate = updateAvailable(useUpdateStatus());
  return (
    <section className="mkt-page mkt-settings">
      <h1>{t("nav.settings")}</h1>
      <nav className="mkt-settings__nav" aria-label={t("nav.settings")}>
        {/* NavLink marks the open tab with aria-current="page". The links are
            absolute: this page renders under the /settings/* splat route, where
            React Router 7 resolves a relative link against the whole URL. */}
        <NavLink to="/settings" end>
          {t("settings.section.account")}
        </NavLink>
        <NavLink to="/settings/sessions">{t("settings.section.sessions")}</NavLink>
        <NavLink to="/settings/appearance">{t("settings.section.appearance")}</NavLink>
        <NavLink to="/settings/privacy">{t("settings.section.privacy")}</NavLink>
        <NavLink to="/settings/models">{t("settings.section.models")}</NavLink>
        <NavLink to="/settings/rules">{t("settings.section.rules")}</NavLink>
        <NavLink
          to="/settings/about"
          className="mkt-settings__navlink"
          aria-describedby={hasUpdate ? "mkt-settings-update-badge" : undefined}
        >
          {t("settings.section.about")}
          {hasUpdate && (
            <span className="mkt-update-dot" aria-hidden="true" data-testid="update-badge" />
          )}
        </NavLink>
        {hasUpdate && (
          <span id="mkt-settings-update-badge" hidden>
            {t("update.badge")}
          </span>
        )}
      </nav>
      <Routes>
        <Route index element={<AccountTab />} />
        <Route path="sessions" element={<SessionsTab />} />
        <Route path="appearance" element={<AppearanceTab />} />
        <Route path="privacy" element={<PrivacyTab />} />
        <Route path="models" element={<ModelsSection />} />
        <Route path="rules" element={<RulesSection />} />
        <Route path="about" element={<AboutTab />} />
        {/* an unknown tab (/settings/nope) opens Account instead of an empty panel */}
        <Route path="*" element={<Navigate to="/settings" replace />} />
      </Routes>
    </section>
  );
}

function AccountTab() {
  const { t } = useI18n();
  const { user, logoutAll } = useAuth();
  const nav = useNavigate();
  const [signOutRefused, setSignOutRefused] = useState(false);
  if (!user) return null;
  return (
    <div className="mkt-settings__panel">
      <p>
        {t("settings.signedInAs")} <strong>{user.username}</strong>
        {user.is_admin && ` (${t("settings.admin")})`}
      </p>
      <Button
        variant="destructive"
        onClick={async () => {
          try {
            await logoutAll();
          } catch {
            // refused: this machine cannot reach its home (lib/auth)
            setSignOutRefused(true);
            return;
          }
          nav("/login", { replace: true });
        }}
      >
        {t("settings.signOutEverywhere")}
      </Button>
      {signOutRefused && (
        <p className="mkt-alert" role="alert">
          {t("homeOffline.signOut")}
        </p>
      )}
      <hr />
      <h2>{t("settings.tokens.title")}</h2>
      <p className="mkt-muted">{t("settings.tokens.hint")}</p>
      <Link to="/account/tokens" className="mkt-link-btn">
        {t("settings.tokens.manage")}
      </Link>
      <hr />
      <DiagnosticsSection />
    </div>
  );
}

// DiagnosticsSection lets any authenticated user download a support
// bundle scoped to their own activity (GET /api/diagnostics/export).
// Admins get the full cross-service bundle from the admin Logs page.
function DiagnosticsSection() {
  const { t } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  return (
    <div className="mkt-settings__diagnostics">
      <h2>{t("settings.diagnostics.title")}</h2>
      <p className="mkt-muted">{t("settings.diagnostics.hint")}</p>
      <Button
        variant="secondary"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          try {
            await downloadBlob("/api/diagnostics/export", "maktaba-diagnostics.tar.gz");
            toast.show({ tone: "success", message: t("settings.diagnostics.success") });
          } catch (e) {
            const detail = e instanceof ApiError ? e.message : t("common.error");
            toast.show({ tone: "error", message: `${t("settings.diagnostics.error")}: ${detail}` });
          } finally {
            setBusy(false);
          }
        }}
      >
        {busy ? t("settings.diagnostics.running") : t("settings.diagnostics.collect")}
      </Button>
    </div>
  );
}

function SessionsTab() {
  const { t } = useI18n();
  return (
    <div className="mkt-settings__panel">
      <h2>{t("settings.sessions.title")}</h2>
      <p className="mkt-muted">{t("settings.sessions.hint")}</p>
      <Link to="/account/sessions" className="mkt-link-btn">
        {t("settings.sessions.manage")}
      </Link>
    </div>
  );
}

function AppearanceTab() {
  const { t, locale, setLocale } = useI18n();
  const [mode, setLocalMode] = useState<ThemeMode>(readMode);
  const [density, setDensity] = useState<Density>(readDensity);

  useEffect(() => {
    applyDensity(density);
  }, [density]);

  return (
    <div className="mkt-settings__panel mkt-settings__appearance">
      <RadioGroup legend={t("settings.theme")} name="theme">
        {(["light", "dark", "system"] as ThemeMode[]).map((opt) => (
          <Radio
            key={opt}
            name="theme"
            label={t(`settings.theme.${opt}`)}
            checked={mode === opt}
            onChange={() => {
              setMode(opt);
              setLocalMode(opt);
            }}
          />
        ))}
      </RadioGroup>

      <RadioGroup legend={t("settings.language")} name="language">
        {(["en", "ar"] as Locale[]).map((opt) => (
          <Radio
            key={opt}
            name="language"
            label={t(`settings.language.${opt}`)}
            checked={locale === opt}
            onChange={() => setLocale(opt)}
          />
        ))}
      </RadioGroup>

      <RadioGroup legend={t("settings.density")} name="density">
        {(["comfortable", "compact"] as Density[]).map((opt) => (
          <Radio
            key={opt}
            name="density"
            label={t(`settings.density.${opt}`)}
            checked={density === opt}
            onChange={() => setDensity(opt)}
          />
        ))}
      </RadioGroup>
    </div>
  );
}

// VersionInfo mirrors GET /api/system/version (Epic 28 fields included).
interface VersionInfo {
  version: string;
  commit?: string;
  build_date?: string;
  channel?: string;
}

/**
 * The release page link, only for http(s) URLs: the value comes from the
 * server, and a `javascript:` or `data:` URL in an href would run when clicked.
 */
export function safeReleaseUrl(raw: unknown): string | null {
  if (typeof raw !== "string" || raw.trim() === "") return null;
  try {
    const u = new URL(raw);
    return u.protocol === "http:" || u.protocol === "https:" ? u.href : null;
  } catch {
    return null; // relative or malformed
  }
}

type UpdatePhase = "idle" | "downloading" | "restarting" | "done" | "error" | "rolled_back";

// AboutTab is the version + update section (Stories 28.1/28.2/28.3/28.6).
// It shows the running build, the update channel and last-check time, an
// "available" card with the release notes rendered as (safe) Markdown,
// and — for admins on a self-updatable install — a one-click "Update
// now". A Docker install is known from the status (`install: "docker"`),
// so its `docker compose pull` instruction shows up front instead of a
// button that can't work; an older server's 409 instruction still shows
// after a click. A self-update the server undid (rolled_back) is reported.
function AboutTab() {
  const { t, formatDate } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const buildVersion = import.meta.env?.VITE_APP_VERSION ?? "dev";

  const [ver, setVer] = useState<VersionInfo | null>(null);
  const shared = useUpdateStatus();
  const [fresh, setFresh] = useState<UpdateStatus | null>(null);
  const upd = fresh ?? shared;
  const [checking, setChecking] = useState(false);
  const [phase, setPhase] = useState<UpdatePhase>("idle");
  const [dockerHint, setDockerHint] = useState<string | null>(null);
  // false once the tab unmounts: stops the post-update polling
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    api
      .get<VersionInfo>("/api/system/version")
      .then(setVer)
      .catch(() => {});
  }, []);

  const checkNow = async () => {
    setChecking(true);
    try {
      setFresh(await fetchUpdateStatus(true));
    } catch (e) {
      const detail = e instanceof ApiError ? e.message : t("common.error");
      toast.show({ tone: "error", message: detail });
    } finally {
      setChecking(false);
    }
  };

  const runUpdate = async () => {
    setPhase("downloading");
    setDockerHint(null);
    try {
      await api.post("/api/admin/system/update", { confirm: true });
      setPhase("restarting");
      // The server re-execs; poll /api/system/version until it answers
      // with a different version. Still the old one at the end: the update
      // was rolled back. Without a loaded version, the update check's
      // current_version is the one to move away from.
      const from = ver?.version || upd?.current_version || "";
      const polled = await pollVersionUntilChanged(
        from,
        upd?.latest_version ?? "",
        () => alive.current
      );
      if (!alive.current) return;
      if (polled.changed) {
        setVer(polled.info);
        setPhase("done");
        fetchUpdateStatus(true)
          .then((s) => alive.current && setFresh(s))
          .catch(() => {});
      } else {
        setPhase("rolled_back");
      }
    } catch (e) {
      if (!alive.current) return;
      if (e instanceof ApiError && e.status === 409) {
        // Docker/package-manager install: surface the instruction.
        const hint =
          (e.problem as unknown as { instructions?: string }).instructions ?? e.problem.detail;
        if (hint) {
          setDockerHint(hint);
          setPhase("idle");
          return;
        }
      }
      setPhase("error");
    }
  };

  const version = ver?.version ?? buildVersion;
  const channel = ver?.channel ?? upd?.channel ?? "stable";
  const docker = upd?.install === "docker" ? upd.instructions || DOCKER_INSTRUCTIONS : dockerHint;
  const showUpdateButton =
    user?.is_admin && upd?.available && phase !== "done" && phase !== "rolled_back" && !docker;
  const lastRollback = phase === "idle" ? upd?.rolled_back : undefined;
  // a failed update can be tried again; only a running one blocks the button
  const updateRunning = phase === "downloading" || phase === "restarting";
  const releaseUrl = safeReleaseUrl(upd?.release_url);

  return (
    <div className="mkt-settings__panel mkt-settings__about">
      <dl className="mkt-about__meta">
        <div>
          <dt>{t("update.current")}</dt>
          <dd>
            <strong>{version}</strong>
          </dd>
        </div>
        {ver?.commit && ver.commit !== "unknown" && (
          <div>
            <dt>{t("update.commit")}</dt>
            <dd>
              <code>{ver.commit.slice(0, 10)}</code>
            </dd>
          </div>
        )}
        {ver?.build_date && ver.build_date !== "unknown" && (
          <div>
            <dt>{t("update.buildDate")}</dt>
            <dd>{ver.build_date}</dd>
          </div>
        )}
        <div>
          <dt>{t("update.channel")}</dt>
          <dd>{t(`update.channel.${channel}`)}</dd>
        </div>
        {upd?.checked_at && (
          <div>
            <dt>{t("update.lastChecked")}</dt>
            <dd>{formatDate(upd.checked_at)}</dd>
          </div>
        )}
      </dl>

      <div className="mkt-about__actions">
        <Button variant="secondary" disabled={checking} onClick={checkNow}>
          {checking ? t("update.checking") : t("update.checkNow")}
        </Button>
      </div>

      {upd?.disabled && <p className="mkt-muted">{t("update.disabled")}</p>}

      {upd && !upd.disabled && !upd.available && (
        <p className="mkt-muted">{t("update.upToDate")}</p>
      )}

      {lastRollback && (
        <p className="mkt-error" role="status">
          {t("update.lastRolledBack", { version: lastRollback.version })}{" "}
          <span className="mkt-muted">{lastRollback.reason}</span>
        </p>
      )}

      {upd?.available && upd.latest_version && (
        <div className="mkt-about__update" role="region" aria-label={t("update.section.title")}>
          <h2>{t("update.available", { version: upd.latest_version })}</h2>
          {upd.release_notes && (
            <details className="mkt-about__notes" open>
              <summary>{t("update.banner.notes")}</summary>
              {/* Markdown as React elements: release-note HTML is shown as text, never injected. */}
              <Markdown className="mkt-about__notes-body mkt-markdown" source={upd.release_notes} />
            </details>
          )}
          {releaseUrl && (
            <p>
              <a href={releaseUrl} target="_blank" rel="noreferrer">
                {t("update.viewRelease")}
              </a>
            </p>
          )}

          {docker && (
            <div className="mkt-about__docker">
              <p>{t("update.docker.instructions")}</p>
              <pre>{docker}</pre>
              {upd.install === "docker" && upd.image && (
                <p className="mkt-muted">
                  {t("update.docker.image")} <code>{upd.image}</code>
                </p>
              )}
            </div>
          )}

          {showUpdateButton && (
            <Button variant="primary" disabled={updateRunning} onClick={runUpdate}>
              {updateRunning ? t(`update.phase.${phase}`) : t("update.updateNow")}
            </Button>
          )}

          {/* progress is announced: a live region for each phase text */}
          {phase === "downloading" && (
            <p className="mkt-muted" role="status">
              {t("update.phase.downloading")}
            </p>
          )}
          {phase === "restarting" && (
            <p className="mkt-muted" role="status">
              {t("update.phase.restarting")}
            </p>
          )}
          {phase === "done" && (
            <p className="mkt-success" role="status">
              {t("update.phase.done", { version: upd.latest_version })}
            </p>
          )}
          {phase === "error" && (
            <p className="mkt-error" role="alert">
              {t("update.phase.error")}
            </p>
          )}
          {phase === "rolled_back" && (
            <p className="mkt-error" role="alert">
              {t("update.rolledBack")}
            </p>
          )}
        </div>
      )}

      {/* after an update the refreshed status no longer offers one: say so here */}
      {phase === "done" && !upd?.available && (
        <p className="mkt-success" role="status">
          {t("update.phase.done", { version })}
        </p>
      )}

      {user?.is_admin && <UpdatesSettings />}
    </div>
  );
}

const DOCKER_INSTRUCTIONS = "docker compose pull && docker compose up -d";

// pollVersionUntilChanged re-fetches /api/system/version until it reports
// a version different from `from` (the server re-execs after a self-
// update), until the server reports `target` as rolled back (its new
// binary could not start and the old one was put back), until a bounded
// number of attempts elapse (changed: false, the update did not take), or
// until `keepGoing` says stop (the tab closed).
export async function pollVersionUntilChanged(
  from: string,
  target: string,
  keepGoing: () => boolean,
  opts: { attempts?: number; delayMs?: number } = {}
): Promise<{ changed: boolean; info: VersionInfo }> {
  const ATTEMPTS = opts.attempts ?? 30;
  const DELAY_MS = opts.delayMs ?? 2000;
  let last: VersionInfo = { version: from };
  for (let i = 0; i < ATTEMPTS && keepGoing(); i++) {
    await new Promise((r) => setTimeout(r, DELAY_MS));
    if (!keepGoing()) break;
    try {
      const v = await api.get<VersionInfo>("/api/system/version");
      last = v;
      if (v.version && v.version !== from) return { changed: true, info: v };
      if (target) {
        const s = await api.get<UpdateStatus>("/api/system/updates").catch(() => null);
        if (s?.rolled_back?.version === target) return { changed: false, info: v };
      }
    } catch {
      /* server mid-restart — keep polling */
    }
  }
  return { changed: false, info: last };
}

// PrivacyTab — Story 29.4 watch-history & privacy. A switch that pauses
// analytics collection (the /api/watch/start handler stops writing rows
// when off), plus a compact view of the user's own recent history with a
// per-item remove (which also clears the resume point — Continue Watching
// and history stay in lockstep).
function PrivacyTab() {
  const { t } = useI18n();
  const toast = useToast();
  const [tracking, setTracking] = useState<boolean | null>(null);
  const [history, setHistory] = useState<HistoryItem[] | null>(null);
  const [busy, setBusy] = useState(false);
  // a failed load is said, not guessed: no "Tracking on" when it may be off,
  // no "Nothing here yet." when the history could not be read
  const [privacyFailed, setPrivacyFailed] = useState(false);
  const [historyFailed, setHistoryFailed] = useState(false);

  useEffect(() => {
    analyticsApi
      .getPrivacy()
      .then((p) => setTracking(Boolean(p?.track_enabled)))
      .catch(() => setPrivacyFailed(true));
    analyticsApi
      .history({ limit: 20 })
      // items null or missing is an empty history
      .then((r) => setHistory(Array.isArray(r?.items) ? r.items : []))
      .catch(() => {
        setHistoryFailed(true);
        setHistory([]);
      });
  }, []);

  async function toggle() {
    if (tracking === null || busy) return;
    setBusy(true);
    const next = !tracking;
    try {
      const res = await analyticsApi.setPrivacy(next);
      setTracking(res.track_enabled);
      toast.show({ tone: "success", message: t("settings.privacy.saved") });
    } catch (e) {
      toast.show({ tone: "error", message: e instanceof ApiError ? e.message : t("common.error") });
    } finally {
      setBusy(false);
    }
  }

  async function remove(videoId: string) {
    try {
      await analyticsApi.deleteHistory(videoId);
      setHistory((h) => (h ? h.filter((it) => it.video_id !== videoId) : h));
    } catch (e) {
      toast.show({ tone: "error", message: e instanceof ApiError ? e.message : t("common.error") });
    }
  }

  return (
    <div className="mkt-settings__panel mkt-settings__privacy">
      <h2>{t("settings.privacy.title")}</h2>
      <p className="mkt-muted">{t("settings.privacy.hint")}</p>
      {privacyFailed && (
        <p className="mkt-error" role="alert">
          {t("common.error")}
        </p>
      )}
      {tracking !== null && (
        <Toggle
          checked={tracking}
          disabled={busy}
          onChange={() => void toggle()}
          label={tracking ? t("settings.privacy.on") : t("settings.privacy.off")}
        />
      )}

      <hr />
      <h2>{t("settings.privacy.history")}</h2>
      {historyFailed ? (
        <p className="mkt-error" role="alert">
          {t("common.error")}
        </p>
      ) : history === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : history.length === 0 ? (
        <p className="mkt-muted">{t("common.empty")}</p>
      ) : (
        <table className="mkt-table" aria-label={t("settings.privacy.history")}>
          <thead>
            <tr>
              <th>{t("analytics.col.video")}</th>
              <th>{t("settings.privacy.progress")}</th>
              <th>{t("settings.privacy.watched")}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {history.map((h) => (
              <tr key={h.video_id}>
                <td dir="auto">
                  <Link to={`/videos/${encodeURIComponent(h.video_id)}`}>
                    {h.title || h.video_id}
                  </Link>
                </td>
                <td>{formatPercent(h.best_percent ?? 0)}</td>
                <td>{formatWatchTime(h.total_watch_sec ?? 0)}</td>
                <td>
                  <Button size="sm" variant="ghost" onClick={() => void remove(h.video_id)}>
                    {t("settings.privacy.remove")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p>
        <Link to="/account/history" className="mkt-link-btn">
          {t("settings.privacy.fullHistory")}
        </Link>
      </p>

      <hr />
      <h2>{t("settings.privacy.activity")}</h2>
      <ActivityTimeline limit={10} />
      <p>
        <Link to="/account/history?tab=activity" className="mkt-link-btn">
          {t("settings.privacy.fullActivity")}
        </Link>
      </p>
    </div>
  );
}
