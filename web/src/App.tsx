// Root component for the Maktaba web app.
//
// Provider order (outer→inner): i18n → theme-sync → toasts → node guard → auth → flags →
// router → shortcuts → shell. ThemeProvider from the Epic-17 design
// system stamps <html data-theme>; ThemeSync keeps it tracking the
// "system" OS preference live (Story 11.8).
import { BrowserRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { AppShell, MoreMenu } from "./components/AppShell";
import { DesktopBridge } from "./components/DesktopBridge";
import { DesktopGate } from "./components/DesktopGate";
import { NodeGuard } from "./components/NodeGuard";
import { RouteErrorBoundary } from "./components/RouteErrorBoundary";
import { I18nProvider } from "./lib/i18n";
import { AuthProvider, RequireAuth } from "./lib/auth";
import { FlagsProvider } from "./lib/flags";
import { ShortcutProvider } from "./lib/keyboard/shortcuts";
import { applyResolvedTheme, readMode, watchSystemTheme } from "./lib/theme";
import { LibraryBrowser } from "./pages/LibraryBrowser";
import { Home } from "./pages/Home";
import { Libraries } from "./pages/Library/Libraries";
import { LibraryPage } from "./pages/Library/LibraryPage";
import { SeriesBrowser } from "./pages/Library/SeriesBrowser";
import { Playlists } from "./pages/Playlists/Playlists";
import { PlaylistPage } from "./pages/Playlists/PlaylistPage";
import { SeriesDetail } from "./pages/Library/SeriesDetail";
import { VideoDetail } from "./pages/VideoDetail";
import { VideoPlayer } from "./pages/VideoPlayer";
import { Search } from "./pages/Search";
import { SearchVideo } from "./pages/SearchVideo";
import { ConnectedDevices } from "./pages/Profile/ConnectedDevices";
import { Sessions } from "./pages/Profile/Sessions";
import { APITokens } from "./pages/Profile/APITokens";
import { WatchHistory } from "./pages/Profile/WatchHistory";
import { Account } from "./pages/Profile/Account";
import { ConnectApp } from "./pages/Profile/ConnectApp";
import { Guide } from "./pages/LiveTV/Guide";
import { Player } from "./pages/LiveTV/Player";
import { Login } from "./pages/Login";
import { Register } from "./pages/Auth/Register";
import { NotFound } from "./pages/NotFound";
import { Pair } from "./pages/Pair";
import { HelpIndex, HelpTopic } from "./help/Help";
import { IS_REMOTE, SERVER_PAGES } from "./lib/edition";
import { serverRoutes } from "./serverRoutes";
import { RemoteHome } from "./pages/RemoteHome";

// Epic 46: big-screen mode, its own full-screen frame and chunk
const BigScreen = lazy(() => import("./bigscreen/BigScreen"));

// ThemeSync re-applies the resolved theme whenever the OS preference
// flips while the user is in "system" mode (Story 11.8 live-toggle AC).
function ThemeSync({ children }: { children: ReactNode }) {
  const [, force] = useState(0);
  useEffect(() => {
    applyResolvedTheme(readMode());
    return watchSystemTheme(() => force((n) => n + 1));
  }, []);
  return <>{children}</>;
}

// The sign-in pages sit outside AppShell, so they get their own error
// boundary: a crash there shows an error instead of blanking the app, and
// moving to another route clears it.
function PublicPages() {
  const { pathname } = useLocation();
  return (
    <RouteErrorBoundary resetKey={pathname}>
      <Outlet />
    </RouteErrorBoundary>
  );
}

export function App() {
  return (
    <I18nProvider>
      <ThemeSync>
        <ToastProvider>
          {/* Epic 36: Maktaba.app's first-run setup / server starting screens */}
          <DesktopGate>
            {/* Epic 35: a paused node shows the paused page instead of the app */}
            <NodeGuard>
              <AuthProvider>
                <FlagsProvider>
                  <BrowserRouter>
                    <DesktopBridge />
                    <ShortcutProvider>
                      <Routes>
                        <Route element={<PublicPages />}>
                          <Route path="/login" element={<Login />} />
                          <Route path="/register" element={<Register />} />
                          <Route
                            path="/big/*"
                            element={
                              <RequireAuth>
                                <Suspense fallback={null}>
                                  <BigScreen />
                                </Suspense>
                              </RequireAuth>
                            }
                          />
                        </Route>
                        <Route
                          element={
                            <RequireAuth>
                              <AppShell />
                            </RequireAuth>
                          }
                        >
                          <Route path="/" element={IS_REMOTE ? <RemoteHome /> : <Home />} />
                          <Route path="/library" element={<Libraries />} />
                          <Route path="/library/:libraryId" element={<LibraryPage />} />
                          <Route path="/videos" element={<LibraryBrowser />} />
                          <Route path="/series" element={<SeriesBrowser />} />
                          <Route path="/playlists" element={<Playlists />} />
                          <Route path="/playlists/:playlistId" element={<PlaylistPage />} />
                          <Route path="/series/:id" element={<SeriesDetail />} />
                          <Route path="/videos/:videoId" element={<VideoDetail />} />
                          <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
                          <Route path="/search" element={<Search />} />
                          <Route path="/search/video/:videoId" element={<SearchVideo />} />
                          <Route path="/guide" element={<Guide />} />
                          <Route path="/live/:number" element={<Player />} />
                          <Route path="/more" element={<MoreMenu />} />
                          <Route path="/help" element={<HelpIndex />} />
                          <Route path="/help/:topic" element={<HelpTopic />} />
                          <Route path="/account" element={<Account />} />
                          <Route path="/account/devices" element={<ConnectedDevices />} />
                          <Route path="/account/connect" element={<ConnectApp />} />
                          <Route path="/account/sessions" element={<Sessions />} />
                          <Route path="/account/tokens" element={<APITokens />} />
                          <Route path="/account/history" element={<WatchHistory />} />
                          <Route path="/pair" element={<Pair />} />
                          {SERVER_PAGES && serverRoutes()}
                          <Route path="*" element={<NotFound />} />
                        </Route>
                      </Routes>
                    </ShortcutProvider>
                  </BrowserRouter>
                </FlagsProvider>
              </AuthProvider>
            </NodeGuard>
          </DesktopGate>
        </ToastProvider>
      </ThemeSync>
    </I18nProvider>
  );
}
