// Auth context wiring (Story 10.2 cookie flow).
//
// Login posts to /api/auth/login WITHOUT the `X-Maktaba-Client: native`
// header so the API sets cookies and returns `{user}`. Logout posts to
// /api/auth/logout (which clears the cookie and revokes the session
// row). The session-restore path on app load probes /api/auth/me; that
// endpoint is owned by Story 10.1 AC-3 — until it lands the SPA
// optimistically assumes "unknown" and lets handlers 401 redirect.
//
// Epic 37: on a node that cannot reach its home, /api/auth/me is answered by
// the node from its own copy of the home's database, so a reload keeps the
// user in, and a sign-in is checked there too: a session valid on that
// machine only (session "node"). Once the home is back, a change asks for a
// sign-in with the home (SIGNIN_REQUIRED_EVENT): the user is signed out here
// and the sign-in page says why. A sign-out of a home session the node refuses
// (503 home_unreachable) keeps the user signed in and throws, so the caller can
// say so (lib/api `isHomeUnreachable`).
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { Navigate, useLocation } from "react-router-dom";
import { api, ApiError, isHomeUnreachable, SIGNIN_REQUIRED_EVENT } from "./api";
import { useI18n } from "./i18n";
import { purgeApiCacheOnLogout } from "./sw";
import { ChangePasswordRequired } from "../pages/Auth/ChangePasswordRequired";

export interface User {
  id: string;
  username: string;
  is_admin: boolean;
  /** An admin reset the password to a temporary one: it must be changed first. */
  must_change_password?: boolean;
  /** "node": signed in on this machine only, while its home was away. */
  session?: "home" | "node";
}

export interface AuthState {
  user: User | null;
  loading: boolean;
  login: (username: string, password: string) => Promise<void>;
  register: (username: string, email: string, password: string) => Promise<void>;
  /** Throws (and keeps the user) when this machine cannot reach its home. */
  logout: () => Promise<void>;
  /** Throws (and keeps the user) when this machine cannot reach its home. */
  logoutAll: () => Promise<void>;
  /** Probe the session again (the home is back: a session may hold). */
  refresh: () => Promise<void>;
  /** The required password change is done: lift the gate. */
  passwordChanged: () => void;
  /** A sign-in made while the home was away ended at a change: sign in with the home. */
  signInAgain?: boolean;
}

// Exported so page tests can sign a user in without the /api/auth/me probe
// (see src/test/pageHarness.tsx).
export const AuthContext = createContext<AuthState | undefined>(undefined);

/** The user from a login / register answer; an answer without one is an error. */
function signedIn(res: { user?: User; session?: User["session"] } | null | undefined): User {
  if (!res?.user) throw new Error("the server's answer has no user");
  return res.session ? { ...res.user, session: res.session } : res.user;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [signInAgain, setSignInAgain] = useState(false);

  const probe = useCallback(async (live: () => boolean) => {
    // Probe the server for an existing session. Endpoint is best-effort:
    // a 401 means "no session", not an error.
    try {
      const me = await api.get<{ user?: User; session?: User["session"] }>("/api/auth/me");
      if (live()) setUser(me?.user ? signedIn(me) : null);
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 401) {
        // log; we still treat as logged-out so the SPA can boot.
        console.warn("auth: /me probe failed", e);
      }
      if (live()) setUser(null);
    } finally {
      if (live()) setLoading(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void probe(() => !cancelled);
    return () => {
      cancelled = true;
    };
  }, [probe]);

  useEffect(() => {
    const ended = () => {
      setSignInAgain(true);
      setUser(null);
    };
    window.addEventListener(SIGNIN_REQUIRED_EVENT, ended);
    return () => window.removeEventListener(SIGNIN_REQUIRED_EVENT, ended);
  }, []);

  const value = useMemo<AuthState>(
    () => ({
      user,
      loading,
      async login(username, password) {
        const res = await api.post<{ user?: User; session?: User["session"] }>("/api/auth/login", {
          username,
          password,
        });
        setUser(signedIn(res));
        setSignInAgain(false);
      },
      async register(username, email, password) {
        // The server signs the new user in (web cookie flow) and returns
        // {user}, mirroring login — so the SPA is authenticated on return.
        const res = await api.post<{ user?: User }>("/api/auth/register", {
          username,
          email,
          password,
        });
        setUser(signedIn(res));
      },
      async logout() {
        try {
          await api.post("/api/auth/logout");
        } catch (e) {
          // the home ends sessions: refused, the session (and its HttpOnly
          // cookie) holds, so the user stays signed in and is told
          if (isHomeUnreachable(e)) throw e;
          // Even if the server call fails, drop our local state — the
          // cookie is HttpOnly and will get cleared by the next 401.
          console.warn("auth: logout call failed", e);
        }
        // Scrub any cached authenticated API responses so the next
        // user on a shared browser cannot be served this user's data.
        await purgeApiCacheOnLogout();
        setUser(null);
      },
      async logoutAll() {
        try {
          await api.post("/api/auth/logout-all");
        } catch (e) {
          if (isHomeUnreachable(e)) throw e;
          console.warn("auth: logout-all call failed", e);
        }
        await purgeApiCacheOnLogout();
        setUser(null);
      },
      async refresh() {
        await probe(() => true);
      },
      passwordChanged() {
        setUser((u) => (u ? { ...u, must_change_password: false } : u));
      },
      signInAgain,
    }),
    [user, loading, probe, signInAgain]
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used inside <AuthProvider>");
  }
  return ctx;
}

/** useAuth for components that may render outside an AuthProvider (null there). */
export function useOptionalAuth(): AuthState | null {
  return useContext(AuthContext) ?? null;
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  const { t } = useI18n();
  const location = useLocation();
  if (loading)
    return (
      <div className="mkt-loading" role="status">
        {t("common.loading")}
      </div>
    );
  // Keep the query too, so /search?q=x comes back to the same search.
  if (!user)
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  // After an admin reset the server refuses everything but the password
  // change, so the app shows only that form until it is done.
  if (user.must_change_password) return <ChangePasswordRequired />;
  return <>{children}</>;
}
