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
import { useToast } from "@ds/components/Toast/Toast";
import { downloadBlob, ApiError } from "../lib/api";

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
        <Link to="models">{t("settings.section.models")}</Link>
        <Link to="about">{t("settings.section.about")}</Link>
      </nav>
      <Routes>
        <Route index element={<AccountTab />} />
        <Route path="sessions" element={<SessionsTab />} />
        <Route path="appearance" element={<AppearanceTab />} />
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

function AboutTab() {
  const { t } = useI18n();
  const version = import.meta.env?.VITE_APP_VERSION ?? "0.1.0";
  return (
    <div className="mkt-settings__panel">
      <p>
        {t("settings.about.version")}: <strong>{String(version)}</strong>
      </p>
    </div>
  );
}
