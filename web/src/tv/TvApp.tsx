// Epic 58 story 58.08 — the TV build: the big screen alone, served by the home
// at /tv/ (`pnpm build:tv`, an older-Chromium target: a 2020 Samsung or LG TV
// runs Chromium 68/69). A Samsung or LG launcher (story 58.09) loads it in an
// iframe; the home serves it from the same release as the web app, so the UI on
// a TV is always the home's own version.
//
// Routes (under the /tv base): /big/* is the big screen in its TV mode; any
// other path goes there. Signed out in a browser, a TV-sized page sends the
// person to the home's sign-in, which brings them back here.
//
// In the launcher (story 58.09: ?bridge=1 inside a frame) /tv/ talks to it over the bridge
// (its hello names the TV, which becomes the client header), signs in by the home's own
// code with bearer tokens kept in the home's origin (session.ts: the frame never gets the
// session cookie), and says when the connection is plain HTTP. Built with --bundle-ui, the
// copy in the package (?home=<the home's address>) reaches the home's API at that address.
import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { BrowserRouter, MemoryRouter, Navigate, Route, Routes } from "react-router-dom";
import { I18nProvider, useI18n } from "../lib/i18n";
import { AuthProvider, useAuth } from "../lib/auth";
import { CapabilitiesProvider } from "../lib/capabilities";
import { setApiBase, setBearerAuth } from "../lib/api";
import { setClientHeaderValue } from "../lib/clientHeader";
import { inLauncher, launcherHello, postToLauncher, startBridge } from "../bigscreen/input/bridge";
import { CodeSignIn } from "./CodeSignIn";
import { TvSession } from "./session";
import { LauncherContext, useLauncher, type LauncherMode } from "./launcher";

const BigScreen = lazy(() => import("../bigscreen/BigScreen"));

/** Where the home's sign-in page is, returning to /tv/ once signed in. */
export const TV_SIGN_IN = `/login?next=${encodeURIComponent("/tv/")}`;

/** How /tv/ runs: in a launcher (and how), or in a browser. */
export function launcherMode(loc: Location = window.location): LauncherMode | null {
  const q = new URLSearchParams(loc.search);
  if (q.get("bridge") !== "1" || !inLauncher()) return null;
  const home = q.get("home");
  let base: string | null = null;
  if (home) {
    try {
      const u = new URL(home);
      if (u.protocol === "https:" || u.protocol === "http:") base = u.origin;
    } catch {
      base = null;
    }
  }
  const lang = q.get("lang");
  return {
    bundled: base !== null,
    base,
    plain: (base ?? loc.origin).startsWith("http:"),
    lang: lang === "ar" || lang === "en" ? lang : null,
  };
}

/** The client header of the launcher's app: tv-web/<version> (<platform>; tv). */
export function launcherClient(version: string, platform: string): string {
  return `tv-web/${version || "dev"} (${platform}; tv)`;
}

export function TvGate({ session = null }: { session?: TvSession | null }) {
  const { user, loading, refresh } = useAuth();
  const { t, dir, locale } = useI18n();
  const launcher = useLauncher();
  if (loading) return <div className="tv-boot" aria-busy="true" />;
  if (session && !user) {
    const h = launcherHello();
    return (
      <CodeSignIn
        session={session}
        deviceLabel={h ? h.model : "TV"}
        plain={!!launcher?.plain}
        onSignedIn={() => void refresh()}
      />
    );
  }
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

/** In a launcher: follows the launcher's language at start, and tells it of a change. */
function LauncherLanguage({ mode }: { mode: LauncherMode }) {
  const { locale, setLocale } = useI18n();
  const [synced, setSynced] = useState(false);
  useEffect(() => {
    if (!synced) {
      setSynced(true);
      if (mode.lang && mode.lang !== locale) {
        setLocale(mode.lang);
        return;
      }
    }
    postToLauncher({ type: "maktaba:setLanguage", lang: locale });
  }, [locale, mode.lang, setLocale, synced]);
  return null;
}

/** Waits for the launcher's hello before the app asks the home anything. */
function LauncherBoot({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(() => launcherHello() !== null);
  useEffect(() => {
    if (ready) return;
    let live = true;
    void startBridge().then((h) => {
      if (!live) return;
      if (h) setClientHeaderValue(launcherClient(h.version, h.platform));
      setReady(true);
    });
    return () => {
      live = false;
    };
  }, [ready]);
  if (!ready) return <div className="tv-boot" aria-busy="true" />;
  return <>{children}</>;
}

let session: TvSession | null = null;

/** Sets up the launcher mode once, before anything asks the home (tests: reset with null). */
export function installLauncherMode(mode: LauncherMode | null): TvSession | null {
  if (!mode) {
    session = null;
    setBearerAuth(null);
    return null;
  }
  if (mode.base) setApiBase(mode.base);
  session = new TvSession();
  setBearerAuth(session);
  return session;
}

export function TvApp({ mode = launcherMode() }: { mode?: LauncherMode | null }) {
  const [tv] = useState(() => installLauncherMode(mode));
  const routes = (
    <Routes>
      <Route path="/big/*" element={<TvGate session={tv} />} />
      <Route path="*" element={<Navigate to="/big" replace />} />
    </Routes>
  );
  const app = (
    <AuthProvider>
      <CapabilitiesProvider>
        {mode?.bundled ? (
          // the package's own page (file://): routes live in memory, not in its path
          <MemoryRouter initialEntries={["/big"]}>{routes}</MemoryRouter>
        ) : (
          <BrowserRouter basename="/tv">{routes}</BrowserRouter>
        )}
      </CapabilitiesProvider>
    </AuthProvider>
  );
  return (
    <I18nProvider>
      <LauncherContext.Provider value={mode}>
        {mode ? (
          <LauncherBoot>
            <LauncherLanguage mode={mode} />
            {app}
          </LauncherBoot>
        ) : (
          app
        )}
      </LauncherContext.Provider>
    </I18nProvider>
  );
}
