// Root component for the Maktaba web app.
//
// Provider order (outer→inner): i18n → theme-sync → toasts → node guard → auth → flags →
// router → shortcuts → shell. ThemeProvider from the Epic-17 design
// system stamps <html data-theme>; ThemeSync keeps it tracking the
// "system" OS preference live (Story 11.8).
import { BrowserRouter, Outlet, Route, Routes, Navigate, useLocation } from "react-router-dom";
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
import { AdminEnrichment } from "./pages/Admin/Enrichment";
import { VideoDetail } from "./pages/VideoDetail";
import { VideoPlayer } from "./pages/VideoPlayer";
import { Search } from "./pages/Search";
import { SearchVideo } from "./pages/SearchVideo";
import { QueueOverview } from "./pages/queue/QueueOverview";
import { QueueDetail } from "./pages/queue/QueueDetail";
import { ItemHistory } from "./pages/queue/ItemHistory";
import { Reports } from "./pages/queue/Reports";
import { Failures } from "./pages/queue/Failures";
import { Transcription } from "./pages/transcription/Transcription";
import { AdminExtensions } from "./pages/Admin/Extensions";
import { Settings } from "./pages/Settings";
import { AdminUsers } from "./pages/Admin/Users";
import { AdminAuditLog } from "./pages/Admin/AuditLog";
import { SystemHealth } from "./pages/Admin/SystemHealth";
import { Machines } from "./pages/Admin/Machines";
import { AdminUpdates } from "./pages/Admin/Updates";
import { AdminLogs } from "./pages/Admin/Logs";
import { CloudDevices } from "./pages/Cloud/Devices";
import { ConnectedDevices } from "./pages/Profile/ConnectedDevices";
import { Sessions } from "./pages/Profile/Sessions";
import { APITokens } from "./pages/Profile/APITokens";
import { WatchHistory } from "./pages/Profile/WatchHistory";
import { Account } from "./pages/Profile/Account";
import { ConnectApp } from "./pages/Profile/ConnectApp";
import { LibraryACL } from "./pages/Admin/LibraryACL";
import { AdminLibraries } from "./pages/Admin/Libraries";
import { Channels } from "./pages/Admin/Channels";
import { FillerPools } from "./pages/Admin/FillerPools";
import { AdminLiveTV } from "./pages/Admin/LiveTV";
import { AdminTvChanges } from "./pages/Admin/LiveTV/Changes";
import { AdminTvChangeDetail } from "./pages/Admin/LiveTV/ChangeDetail";
import { AdminAnalytics } from "./pages/Admin/Analytics";
import { Guide } from "./pages/LiveTV/Guide";
import { Player } from "./pages/LiveTV/Player";
import { ServerStatus } from "./pages/Cloud/ServerStatus";
import { Login } from "./pages/Login";
import { Register } from "./pages/Auth/Register";
import { NotFound } from "./pages/NotFound";
import { Pair } from "./pages/Pair";
import { HelpIndex, HelpTopic } from "./help/Help";

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
                          <Route path="/" element={<Home />} />
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
                          <Route path="/queue" element={<QueueOverview />} />
                          <Route path="/queue/items/:kind/:id" element={<ItemHistory />} />
                          <Route path="/queue/reports" element={<Reports />} />
                          <Route path="/queue/failures" element={<Failures />} />
                          <Route path="/queue/:stage" element={<QueueDetail />} />
                          <Route path="/transcription" element={<Transcription />} />
                          <Route path="/settings/*" element={<Settings />} />
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
                          <Route
                            path="/billing"
                            element={<Navigate to="/settings/subscription" replace />}
                          />
                          <Route path="/cloud/status" element={<ServerStatus />} />
                          <Route path="/admin/libraries" element={<AdminLibraries />} />
                          <Route path="/admin/users" element={<AdminUsers />} />
                          <Route path="/admin/library-acl" element={<LibraryACL />} />
                          <Route path="/admin/extensions" element={<AdminExtensions />} />
                          <Route path="/admin/enrichment" element={<AdminEnrichment />} />
                          <Route path="/admin/channels" element={<Channels />} />
                          <Route path="/admin/filler" element={<FillerPools />} />
                          <Route path="/admin/live-tv" element={<AdminLiveTV />} />
                          <Route path="/admin/live-tv/changes" element={<AdminTvChanges />} />
                          <Route
                            path="/admin/live-tv/changes/:id"
                            element={<AdminTvChangeDetail />}
                          />
                          <Route path="/admin/analytics" element={<AdminAnalytics />} />
                          <Route path="/admin/audit" element={<AdminAuditLog />} />
                          <Route path="/admin/health" element={<SystemHealth />} />
                          <Route path="/admin/machines" element={<Machines />} />
                          <Route path="/admin/updates" element={<AdminUpdates />} />
                          <Route path="/admin/logs" element={<AdminLogs />} />
                          <Route path="/admin/devices" element={<CloudDevices />} />
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
