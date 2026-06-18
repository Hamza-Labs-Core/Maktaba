// Story 11.6 — Settings.
//
// Implemented (genuinely buildable against the live API):
//   Account     — sign-out-everywhere (POST /api/auth/logout-all, wired)
//   Appearance  — theme light/dark/system (11.8), language en/ar (11.12),
//                 density comfortable/compact (persisted)
//   About       — app version
//
// Honestly deferred (no backend endpoint exists — Epic 10/auth, NOT
// Epic 11):
//   Sessions    — 11.14: there is NO GET /api/me/sessions /
//                 DELETE /api/me/sessions/{id}. Only the admin
//                 DELETE /api/users/{id}/sessions/{sid} exists. Showing
//                 a fake list against a dead route would be a false
//                 green, so this panel states the dependency instead.
//   PAT tokens  — 11.13: no personal_access_tokens table / endpoint /
//                 verifier exists anywhere in api/internal. Same call.
import { useEffect, useState } from "react";
import { Link, Route, Routes, useNavigate } from "react-router-dom";
import { RadioGroup, Radio } from "@ds/components/Choice/Radio";
import { Button } from "@ds/components/Button/Button";
import { useAuth } from "../lib/auth";
import { useI18n, type Locale } from "../lib/i18n";
import { readMode, setMode, type ThemeMode } from "../lib/theme";
import { ModelsSection } from "./Settings/ModelsSection";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { api, downloadBlob, ApiError } from "../lib/api";
import { analyticsApi, formatPercent, formatWatchTime, type HistoryItem } from "../lib/analytics";

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
  return (
    <section className="mkt-page mkt-settings">
      <h1>{t("nav.settings")}</h1>
      <nav className="mkt-settings__nav" aria-label={t("nav.settings")}>
        <Link to="">{t("settings.section.account")}</Link>
        <Link to="sessions">{t("settings.section.sessions")}</Link>
        <Link to="appearance">{t("settings.section.appearance")}</Link>
        <Link to="privacy">{t("settings.section.privacy")}</Link>
        <Link to="models">{t("settings.section.models")}</Link>
        <Link to="about">{t("settings.section.about")}</Link>
      </nav>
      <Routes>
        <Route index element={<AccountTab />} />
        <Route path="sessions" element={<SessionsTab />} />
        <Route path="appearance" element={<AppearanceTab />} />
        <Route path="privacy" element={<PrivacyTab />} />
        <Route path="models" element={<ModelsSection />} />
        <Route path="about" element={<AboutTab />} />
      </Routes>
    </section>
  );
}

function AccountTab() {
  const { t } = useI18n();
  const { user, logoutAll } = useAuth();
  const nav = useNavigate();
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
          await logoutAll();
          nav("/login", { replace: true });
        }}
      >
        {t("settings.signOutEverywhere")}
      </Button>
      <hr />
      {/* 11.13 deferred — no backend endpoint. */}
      <p className="mkt-muted">{t("settings.tokens.deferred")}</p>
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
  // 11.14 deferred — no GET /api/me/sessions. Stating the dependency
  // truthfully beats rendering a list against a 404 route.
  return (
    <div className="mkt-settings__panel">
      <p className="mkt-muted">{t("settings.sessions.deferred")}</p>
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

// UpdateStatus mirrors GET /api/system/updates (Story 28.2).
interface UpdateStatus {
  available: boolean;
  disabled?: boolean;
  current_version: string;
  latest_version?: string;
  channel: string;
  release_url?: string;
  release_notes?: string;
  checked_at: string;
}

type UpdatePhase = "idle" | "downloading" | "restarting" | "done" | "error" | "rolled_back";

// AboutTab is the version + update section (Stories 28.1/28.2/28.3/28.6).
// It shows the running build, the update channel and last-check time, an
// "available" card with release notes, and — for admins on a self-
// updatable install — a one-click "Update now" button. Docker/deb
// installs surface the package-manager instruction returned by the 409
// instead of a button that can't work.
function AboutTab() {
  const { t } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const buildVersion = import.meta.env?.VITE_APP_VERSION ?? "dev";

  const [ver, setVer] = useState<VersionInfo | null>(null);
  const [upd, setUpd] = useState<UpdateStatus | null>(null);
  const [checking, setChecking] = useState(false);
  const [phase, setPhase] = useState<UpdatePhase>("idle");
  const [dockerHint, setDockerHint] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<VersionInfo>("/api/system/version")
      .then(setVer)
      .catch(() => {});
    api
      .get<UpdateStatus>("/api/system/updates")
      .then(setUpd)
      .catch(() => {});
  }, []);

  const checkNow = async () => {
    setChecking(true);
    try {
      setUpd(await api.get<UpdateStatus>("/api/system/updates?refresh=true"));
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
      // with a different version (or the rolled-back original).
      const newVer = await pollVersionUntilChanged(ver?.version ?? "");
      setVer(newVer);
      setPhase("done");
    } catch (e) {
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
  const showUpdateButton = user?.is_admin && upd?.available && phase !== "done" && !dockerHint;

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
            <dd>{new Date(upd.checked_at).toLocaleString()}</dd>
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

      {upd?.available && upd.latest_version && (
        <div className="mkt-about__update" role="region" aria-label={t("update.section.title")}>
          <h2>{t("update.available", { version: upd.latest_version })}</h2>
          {upd.release_notes && (
            <details className="mkt-about__notes">
              <summary>{t("update.viewRelease")}</summary>
              {/* Plain-text render — never inject release-note HTML. */}
              <pre className="mkt-about__notes-body">{upd.release_notes}</pre>
            </details>
          )}
          {upd.release_url && (
            <p>
              <a href={upd.release_url} target="_blank" rel="noreferrer">
                {t("update.viewRelease")}
              </a>
            </p>
          )}

          {dockerHint && (
            <div className="mkt-about__docker">
              <p>{t("update.docker.instructions")}</p>
              <pre>{dockerHint}</pre>
            </div>
          )}

          {showUpdateButton && (
            <Button variant="primary" disabled={phase !== "idle"} onClick={runUpdate}>
              {phase === "idle" ? t("update.updateNow") : t(`update.phase.${phase}`)}
            </Button>
          )}

          {phase === "downloading" && <p className="mkt-muted">{t("update.phase.downloading")}</p>}
          {phase === "restarting" && <p className="mkt-muted">{t("update.phase.restarting")}</p>}
          {phase === "done" && (
            <p className="mkt-success">{t("update.phase.done", { version: upd.latest_version })}</p>
          )}
          {phase === "error" && <p className="mkt-error">{t("update.phase.error")}</p>}
        </div>
      )}
    </div>
  );
}

// pollVersionUntilChanged re-fetches /api/system/version until it reports
// a version different from `from` (the server re-execs after a self-
// update), or until a bounded number of attempts elapse.
async function pollVersionUntilChanged(from: string): Promise<VersionInfo> {
  const ATTEMPTS = 30;
  const DELAY_MS = 2000;
  let last: VersionInfo = { version: from };
  for (let i = 0; i < ATTEMPTS; i++) {
    await new Promise((r) => setTimeout(r, DELAY_MS));
    try {
      const v = await api.get<VersionInfo>("/api/system/version");
      last = v;
      if (v.version && v.version !== from) return v;
    } catch {
      /* server mid-restart — keep polling */
    }
  }
  return last;
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

  useEffect(() => {
    analyticsApi
      .getPrivacy()
      .then((p) => setTracking(p.track_enabled))
      .catch(() => setTracking(true));
    analyticsApi
      .history({ limit: 20 })
      .then((r) => setHistory(r.items))
      .catch(() => setHistory([]));
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
      {history === null ? (
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
                  <Link to={`/videos/${h.video_id}`}>{h.title}</Link>
                </td>
                <td>{formatPercent(h.best_percent)}</td>
                <td>{formatWatchTime(h.total_watch_sec)}</td>
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
    </div>
  );
}
