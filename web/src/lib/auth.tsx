// Auth context wiring (Story 10.2 cookie flow).
//
// Login posts to /api/auth/login WITHOUT the `X-Maktaba-Client: native`
// header so the API sets cookies and returns `{user}`. Logout posts to
// /api/auth/logout (which clears the cookie and revokes the session
// row). The session-restore path on app load probes /api/auth/me; that
// endpoint is owned by Story 10.1 AC-3 — until it lands the SPA
// optimistically assumes "unknown" and lets handlers 401 redirect.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { api, ApiError } from './api';

export interface User {
  id: string;
  username: string;
  is_admin: boolean;
}

interface AuthState {
  user: User | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  logoutAll: () => Promise<void>;
}

const AuthContext = createContext<AuthState | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Probe the server for an existing session. Endpoint is best-effort:
    // a 401 means "no session", not an error.
    let cancelled = false;
    (async () => {
      try {
        const me = await api.get<{ user: User }>('/api/auth/me');
        if (!cancelled) setUser(me.user);
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 401) {
          // log; we still treat as logged-out so the SPA can boot.
          console.warn('auth: /me probe failed', e);
        }
        if (!cancelled) setUser(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const value = useMemo<AuthState>(() => ({
    user,
    loading,
    async login(username, password) {
      const res = await api.post<{ user: User }>('/api/auth/login', { username, password });
      setUser(res.user);
    },
    async logout() {
      try {
        await api.post('/api/auth/logout');
      } catch (e) {
        // Even if the server call fails, drop our local state — the
        // cookie is HttpOnly and will get cleared by the next 401.
        console.warn('auth: logout call failed', e);
      }
      setUser(null);
    },
    async logoutAll() {
      try {
        await api.post('/api/auth/logout-all');
      } catch (e) {
        console.warn('auth: logout-all call failed', e);
      }
      setUser(null);
    },
  }), [user, loading]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error('useAuth must be used inside <AuthProvider>');
  }
  return ctx;
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  const location = useLocation();
  if (loading) return <div className="mkt-loading" role="status">Loading…</div>;
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <>{children}</>;
}
