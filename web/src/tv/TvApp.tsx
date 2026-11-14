// Epic 58 story 58.08 — the TV build: the big screen alone, served by the home
// at /tv/ (`pnpm build:tv`, an older-Chromium target: a 2020 Samsung or LG TV
// runs Chromium 68/69). A Samsung or LG launcher (story 58.09) loads it in an
// iframe; the home serves it from the same release as the web app, so the UI on
// a TV is always the home's own version.
//
// Routes (under the /tv base): /big/* is the big screen in its TV mode; any
// other path goes there. Signed out, a TV-sized page sends the person to the
// home's sign-in, which brings them back here (the launcher's own sign-in, a
// code on the TV, is story 58.09's).
import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { I18nProvider, useI18n } from "../lib/i18n";
import { AuthProvider, useAuth } from "../lib/auth";
import { CapabilitiesProvider } from "../lib/capabilities";

const BigScreen = lazy(() => import("../bigscreen/BigScreen"));

/** Where the home's sign-in page is, returning to /tv/ once signed in. */
export const TV_SIGN_IN = `/login?next=${encodeURIComponent("/tv/")}`;

export function TvGate() {
  const { user, loading } = useAuth();
  const { t, dir, locale } = useI18n();
  if (loading) return <div className="tv-boot" aria-busy="true" />;
  if (!user || user.must_change_password)
    return (
      <main className="tv-signin" dir={dir} lang={locale} data-testid="tv-signin">
        <h1>{t("tv.signIn.title")}</h1>
        <p>{t("tv.signIn.body")}</p>
        <button
          type="button"
          // the only control: OK on the remote presses it
          autoFocus
          onClick={() => window.location.assign(TV_SIGN_IN)}
        >
          {t("tv.signIn.go")}
        </button>
      </main>
    );
  return (
    <Suspense fallback={<div className="tv-boot" aria-busy="true" />}>
      <BigScreen mode="tv" />
    </Suspense>
  );
}

export function TvApp() {
  return (
    <I18nProvider>
      <AuthProvider>
        <CapabilitiesProvider>
          <BrowserRouter basename="/tv">
            <Routes>
              <Route path="/big/*" element={<TvGate />} />
              <Route path="*" element={<Navigate to="/big" replace />} />
            </Routes>
          </BrowserRouter>
        </CapabilitiesProvider>
      </AuthProvider>
    </I18nProvider>
  );
}
