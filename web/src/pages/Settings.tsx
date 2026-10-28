// Story 11.6 — Settings. Account, Sessions, Appearance and Privacy are the
// viewer's own panels (pages/Profile/Preferences, Epic 42.5), shared with the
// player's Preferences page; the rest are server pages.
//
//   Account     — sign-out-everywhere (POST /api/auth/logout-all), a link
//                 to personal access tokens (/account/tokens, 11.13) and
//                 the diagnostics bundle
//   Sessions    — a link to the per-session list (/account/sessions, 11.14)
//   Appearance  — theme light/dark/system (11.8), language en/ar (11.12),
//                 density comfortable/compact (persisted)
//   Privacy     — tracking switch, recent history, activity timeline (29.4)
//   Advanced    — Experiments: beta cohorts the user may join (16.6)
//   Storage & discovery — server.toml's path map, roots, mdns, addresses,
//                 public URLs and a restart (admins; Epic 45)
//   Subscription — the plan, users against seats, the license, Maktaba
//                 Cloud (Epic 16; admins manage it, viewers read it)
//   About       — what the app is: version and build, the home, the plan, links,
//                 credits; updates in one line (the Updates page has the rest)
import { Navigate, NavLink, Route, Routes } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { ModelsSection } from "./Settings/ModelsSection";
import { RulesSection } from "./Settings/RulesSection";
import { AppUpdateSettings } from "./Settings/AppUpdateSettings";
import { AboutPanel } from "./Settings/AboutPanel";
import { HomeNameSettings } from "./Settings/HomeNameSettings";
import { ExperimentsSection } from "./Settings/ExperimentsSection";
import { ServerUsageData } from "./Settings/ServerUsageData";
import { SubscriptionSettings } from "./Settings/SubscriptionSettings";
import { StorageSettings } from "./Settings/StorageSettings";
import { AccountPanel, AppearancePanel, PrivacyPanel, SessionsPanel } from "./Profile/Preferences";
import { updateAvailable, useUpdateStatus } from "../lib/updates";
import { TitleRow } from "../help/HelpLink";

export function Settings() {
  const { t } = useI18n();
  const { user } = useAuth();
  const hasUpdate = updateAvailable(useUpdateStatus());
  return (
    <section className="mkt-page mkt-settings">
      <TitleRow>
        <h1>{t("nav.settings")}</h1>
      </TitleRow>
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
        <NavLink to="/settings/advanced">{t("settings.section.advanced")}</NavLink>
        {user?.is_admin && (
          <NavLink to="/settings/storage">{t("settings.section.storage")}</NavLink>
        )}
        <NavLink to="/settings/subscription">{t("settings.section.subscription")}</NavLink>
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
        <Route path="sessions" element={<SessionsPanel />} />
        <Route path="appearance" element={<AppearancePanel />} />
        <Route path="privacy" element={<PrivacyTab />} />
        <Route path="models" element={<ModelsSection />} />
        <Route path="rules" element={<RulesSection />} />
        <Route path="advanced" element={<AdvancedTab />} />
        <Route path="storage" element={<StorageSettings />} />
        <Route path="subscription" element={<SubscriptionSettings />} />
        <Route path="about" element={<AboutPanel />} />
        {/* an unknown tab (/settings/nope) opens Account instead of an empty panel */}
        <Route path="*" element={<Navigate to="/settings" replace />} />
      </Routes>
    </section>
  );
}

// Account and Privacy are the viewer's panels (Profile/Preferences, which the player
// shows too) with the admins' parts added here: the home's name, the server's usage
// data. The API refuses non-admins on both whatever the page shows.
function AccountTab() {
  const { user } = useAuth();
  return (
    <AccountPanel>
      {user?.is_admin && (
        <>
          <hr />
          <HomeNameSettings />
        </>
      )}
    </AccountPanel>
  );
}

function PrivacyTab() {
  const { user } = useAuth();
  return <PrivacyPanel serverUsage={user?.is_admin ? <ServerUsageData /> : undefined} />;
}

// Advanced: the beta cohorts, and how often this app looks for a new version of itself
// (the sideloaded mobile app only).
function AdvancedTab() {
  return (
    <div className="mkt-settings__panel">
      <ExperimentsSection />
      <AppUpdateSettings />
    </div>
  );
}
