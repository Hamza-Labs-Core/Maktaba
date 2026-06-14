// Root component for the Maktaba web app.
//
// Provider order (outer→inner): i18n → theme-sync → toasts → auth →
// router → shortcuts → shell. ThemeProvider from the Epic-17 design
// system stamps <html data-theme>; ThemeSync keeps it tracking the
// "system" OS preference live (Story 11.8).
import { BrowserRouter, Route, Routes, Navigate } from "react-router-dom";
import { useEffect, useState, type ReactNode } from "react";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { AppShell } from "./components/AppShell";
import { DesktopBridge } from "./components/DesktopBridge";
import { I18nProvider } from "./lib/i18n";
import { AuthProvider, RequireAuth } from "./lib/auth";
import { ShortcutProvider } from "./lib/keyboard/shortcuts";
import { applyResolvedTheme, readMode, watchSystemTheme } from "./lib/theme";
import { LibraryBrowser } from "./pages/LibraryBrowser";
import { SeriesBrowser } from "./pages/Library/SeriesBrowser";
import { SeriesDetail } from "./pages/Library/SeriesDetail";
import { AdminEnrichment } from "./pages/Admin/Enrichment";
import { VideoDetail } from "./pages/VideoDetail";
import { VideoPlayer } from "./pages/VideoPlayer";
import { Search } from "./pages/Search";
import { ProcessingQueue } from "./pages/ProcessingQueue";
import { Settings } from "./pages/Settings";
import { AdminUsers } from "./pages/Admin/Users";
import { AdminAuditLog } from "./pages/Admin/AuditLog";
import { SystemHealth } from "./pages/Admin/SystemHealth";
import { AdminLogs } from "./pages/Admin/Logs";
import { CloudDevices } from "./pages/Cloud/Devices";
import { Billing } from "./pages/Cloud/Billing";
import { ConnectedDevices } from "./pages/Profile/ConnectedDevices";
import { Sessions } from "./pages/Profile/Sessions";
import { APITokens } from "./pages/Profile/APITokens";
import { Account } from "./pages/Profile/Account";
import { LibraryACL } from "./pages/Admin/LibraryACL";
import { Channels } from "./pages/Admin/Channels";
import { Guide } from "./pages/LiveTV/Guide";
import { Player } from "./pages/LiveTV/Player";
import { ServerStatus } from "./pages/Cloud/ServerStatus";
import { Login } from "./pages/Login";
import { Register } from "./pages/Auth/Register";
import { ForgotPassword } from "./pages/Auth/ForgotPassword";
import { ResetPassword } from "./pages/Auth/ResetPassword";
import { NotFound } from "./pages/NotFound";

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

export function App() {
  return (
    <I18nProvider>
      <ThemeSync>
        <ToastProvider>
          <AuthProvider>
            <BrowserRouter>
              <DesktopBridge />
              <ShortcutProvider>
                <Routes>
                  <Route path="/login" element={<Login />} />
                  <Route path="/register" element={<Register />} />
                  <Route path="/forgot-password" element={<ForgotPassword />} />
                  <Route path="/reset-password" element={<ResetPassword />} />
                  <Route
                    element={
                      <RequireAuth>
                        <AppShell />
                      </RequireAuth>
                    }
                  >
                    <Route index element={<Navigate to="/library" replace />} />
                    <Route path="/library" element={<LibraryBrowser />} />
                    <Route path="/library/:libraryId" element={<LibraryBrowser />} />
                    <Route path="/series" element={<SeriesBrowser />} />
                    <Route path="/series/:id" element={<SeriesDetail />} />
                    <Route path="/videos/:videoId" element={<VideoDetail />} />
                    <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
                    <Route path="/search" element={<Search />} />
                    <Route path="/guide" element={<Guide />} />
                    <Route path="/live/:number" element={<Player />} />
                    <Route path="/queue" element={<ProcessingQueue />} />
                    <Route path="/settings/*" element={<Settings />} />
                    <Route path="/account" element={<Account />} />
                    <Route path="/account/devices" element={<ConnectedDevices />} />
                    <Route path="/account/sessions" element={<Sessions />} />
                    <Route path="/account/tokens" element={<APITokens />} />
                    <Route path="/billing" element={<Billing />} />
                    <Route path="/cloud/status" element={<ServerStatus />} />
                    <Route path="/admin/users" element={<AdminUsers />} />
                    <Route path="/admin/library-acl" element={<LibraryACL />} />
                    <Route path="/admin/enrichment" element={<AdminEnrichment />} />
                    <Route path="/admin/channels" element={<Channels />} />
                    <Route path="/admin/audit" element={<AdminAuditLog />} />
                    <Route path="/admin/health" element={<SystemHealth />} />
                    <Route path="/admin/logs" element={<AdminLogs />} />
                    <Route path="/admin/devices" element={<CloudDevices />} />
                    <Route path="*" element={<NotFound />} />
                  </Route>
                </Routes>
              </ShortcutProvider>
            </BrowserRouter>
          </AuthProvider>
        </ToastProvider>
      </ThemeSync>
    </I18nProvider>
  );
}
