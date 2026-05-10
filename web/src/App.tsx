// Root component for the Maktaba web app.
//
// Wires the router, the i18n provider, and the auth-aware app shell.
// Page-level components live under src/pages/.
import { BrowserRouter, Route, Routes, Navigate } from 'react-router-dom';
import { AppShell } from './components/AppShell';
import { I18nProvider } from './lib/i18n';
import { AuthProvider, RequireAuth } from './lib/auth';
import { LibraryBrowser } from './pages/LibraryBrowser';
import { VideoDetail } from './pages/VideoDetail';
import { VideoPlayer } from './pages/VideoPlayer';
import { Search } from './pages/Search';
import { ProcessingQueue } from './pages/ProcessingQueue';
import { Settings } from './pages/Settings';
import { Login } from './pages/Login';
import { NotFound } from './pages/NotFound';

export function App() {
  return (
    <I18nProvider>
      <AuthProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
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
              <Route path="/videos/:videoId" element={<VideoDetail />} />
              <Route path="/videos/:videoId/watch" element={<VideoPlayer />} />
              <Route path="/search" element={<Search />} />
              <Route path="/queue" element={<ProcessingQueue />} />
              <Route path="/settings/*" element={<Settings />} />
              <Route path="*" element={<NotFound />} />
            </Route>
          </Routes>
        </BrowserRouter>
      </AuthProvider>
    </I18nProvider>
  );
}
