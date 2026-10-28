// Preferences: a person's own settings, in every edition (Epic 42.5).
//
// The player has no Settings (its server pages stay out of the bundle: the check in
// vite.config.ts fails a player build carrying pages/Settings*), and in the full
// edition Settings sits under Manage, for admins. So a viewer's own choices live
// here, at /account/preferences, in every edition's account pages:
//
//   Appearance  theme, language, density (stored in this browser)
//   Privacy     activity tracking, this app's usage data, recent history and activity
//   Account     Sign out everywhere, API tokens, the diagnostics bundle
//
// Settings (full and remote) shows the same panels in its own tabs, with the admin
// parts passed in (the home's name, the server's usage data), so this module never
// imports a server page.
import { useEffect, useState, type ReactNode } from "react";
import { Link, Navigate, NavLink, Route, Routes, useNavigate } from "react-router-dom";
import { RadioGroup, Radio } from "@ds/components/Choice/Radio";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { useAuth } from "../../lib/auth";
import { useI18n, type Locale } from "../../lib/i18n";
import { readMode, setMode, type ThemeMode } from "../../lib/theme";
import { downloadBlob } from "../../lib/api";
import {
  analyticsApi,
  formatPercent,
  formatWatchTime,
  type HistoryItem,
} from "../../lib/analytics";
import { TitleRow } from "../../help/HelpLink";
import { ActivityTimeline } from "./WatchHistory";
import { TelemetrySettings } from "./TelemetrySettings";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { forgetAnswers, VIDEO_ANSWERS } from "../../lib/pageCache";
import { Loading, TablePlaceholder } from "../../components/Placeholders";

/** Where a person's own preferences live, in every edition. */
export const PREFERENCES_PATH = "/account/preferences";

export function Preferences() {
  const { t } = useI18n();
  return (
    <section className="mkt-page mkt-settings mkt-preferences">
      <TitleRow>
        <h1>{t("nav.preferences")}</h1>
      </TitleRow>
      <nav className="mkt-settings__nav" aria-label={t("nav.preferences")}>
        {/* absolute links: under a splat route a relative one resolves against the URL */}
        <NavLink to={PREFERENCES_PATH} end>
          {t("settings.section.appearance")}
        </NavLink>
        <NavLink to={`${PREFERENCES_PATH}/privacy`}>{t("settings.section.privacy")}</NavLink>
        <NavLink to={`${PREFERENCES_PATH}/account`}>{t("settings.section.account")}</NavLink>
      </nav>
      <Routes>
        <Route index element={<AppearancePanel />} />
        <Route path="privacy" element={<PrivacyPanel />} />
        <Route path="account" element={<AccountPanel />} />
        <Route path="*" element={<Navigate to={PREFERENCES_PATH} replace />} />
      </Routes>
    </section>
  );
}

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

/**
 * Account: who is signed in, Sign out everywhere, API tokens and the diagnostics
 * bundle. `children` comes after Sign out everywhere (Settings puts the admins' home
 * name there).
 */
export function AccountPanel({ children }: { children?: ReactNode }) {
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
          let scope: "everywhere" | "here" | "unchecked";
          try {
            scope = await logoutAll();
          } catch {
            // refused: this machine cannot reach its home (lib/auth)
            setSignOutRefused(true);
            return;
          }
          // only this machine's sessions ended, or the others were not checked: the sign-in
          // page says which
          nav("/login", {
            replace: true,
            state: scope === "everywhere" ? undefined : { signedOut: scope },
          });
        }}
      >
        {t("settings.signOutEverywhere")}
      </Button>
      {signOutRefused && (
        <p className="mkt-alert" role="alert">
          {t("homeOffline.signOut")}
        </p>
      )}
      {children}
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
            const detail = errorText(e, t);
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

export function SessionsPanel() {
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

export function AppearancePanel() {
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

// PrivacyPanel — Story 29.4 watch-history & privacy. A switch that pauses
// analytics collection (the /api/watch/start handler stops writing rows
// when off), plus a compact view of the user's own recent history with a
// per-item remove (which also clears the resume point — Continue Watching
// and history stay in lockstep). Settings passes the server's usage-data switch
// (`serverUsage`) for an admin.
export function PrivacyPanel({ serverUsage }: { serverUsage?: ReactNode }) {
  const { t } = useI18n();
  const toast = useToast();
  // a return shows the last switch and history at once and reads them
  // again quietly (lib/usePageData)
  const {
    data: tracking,
    err: privacyErr,
    setData: setTracking,
  } = usePageData<boolean>("account:privacy", () =>
    analyticsApi.getPrivacy().then((p) => Boolean(p?.track_enabled))
  );
  const {
    data: history,
    err: historyErr,
    loading: historyLoading,
    setData: setHistory,
  } = usePageData<HistoryItem[]>("history:recent", () =>
    analyticsApi
      .history({ limit: 20 })
      // items null or missing is an empty history
      .then((r) => (Array.isArray(r?.items) ? r.items : []))
  );
  const [busy, setBusy] = useState(false);
  // a failed load is said, not guessed: no "Tracking on" when it may be off,
  // no "Nothing here yet." when the history could not be read
  const privacyFailed = privacyErr != null;
  const historyFailed = historyErr != null;

  async function toggle() {
    if (tracking === null || busy) return;
    setBusy(true);
    const next = !tracking;
    try {
      const res = await analyticsApi.setPrivacy(next);
      setTracking(res.track_enabled);
      toast.show({ tone: "success", message: t("settings.privacy.saved") });
    } catch (e) {
      toast.show({ tone: "error", message: errorText(e, t) });
    } finally {
      setBusy(false);
    }
  }

  async function remove(videoId: string) {
    try {
      await analyticsApi.deleteHistory(videoId);
      // the resume point went with it: every kept answer showing the
      // video's progress (Continue watching, the video, the history) is stale
      forgetAnswers(...VIDEO_ANSWERS);
      setHistory((h) => (h ? h.filter((it) => it.video_id !== videoId) : h));
    } catch (e) {
      toast.show({ tone: "error", message: errorText(e, t) });
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
      <TelemetrySettings server={serverUsage} />

      <hr />
      <h2>{t("settings.privacy.history")}</h2>
      {historyFailed ? (
        <p className="mkt-error" role="alert">
          {t("common.error")}
        </p>
      ) : history === null ? (
        <Loading loading={historyLoading}>
          <TablePlaceholder head columns={4} rows={3} />
        </Loading>
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
