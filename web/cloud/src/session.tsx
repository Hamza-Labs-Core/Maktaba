// The signed-in cloud account. On start the app tries POST /v1/auth/refresh
// (the httpOnly cookie) to restore a session; the access token itself stays
// in api.ts's memory.
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  api,
  onSessionChange,
  refreshSession,
  setAccessToken,
  type AuthResult,
  type CloudUser,
} from "./api";

export type SessionStatus = "loading" | "signed_in" | "signed_out";

interface SessionState {
  status: SessionStatus;
  user: CloudUser | null;
  /** POST /v1/auth/login; throws CloudApiError. */
  signIn: (email: string, password: string) => Promise<CloudUser>;
  /** POST /v1/auth/logout and forget the session. */
  signOut: () => Promise<void>;
  /** Forget the session locally (the server already revoked it). */
  forget: () => void;
  /** Refresh from the cookie; true when a session came back. */
  restore: () => Promise<boolean>;
  updateUser: (patch: Partial<CloudUser>) => void;
}

const SessionContext = createContext<SessionState | undefined>(undefined);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<SessionStatus>("loading");
  const [user, setUser] = useState<CloudUser | null>(null);

  const apply = useCallback((r: AuthResult | null) => {
    if (r) {
      setUser(r.user);
      setStatus("signed_in");
    } else {
      setUser(null);
      setStatus("signed_out");
    }
  }, []);

  useEffect(() => onSessionChange(apply), [apply]);

  const restore = useCallback(async () => {
    const r = await refreshSession();
    apply(r);
    return r !== null;
  }, [apply]);

  useEffect(() => {
    void restore();
  }, [restore]);

  const signIn = useCallback(
    async (email: string, password: string) => {
      const r = await api.post<AuthResult>("/v1/auth/login", { email, password }, { auth: false });
      setAccessToken(r.access_token);
      apply(r);
      return r.user;
    },
    [apply]
  );

  const forget = useCallback(() => {
    setAccessToken(null);
    apply(null);
  }, [apply]);

  const signOut = useCallback(async () => {
    try {
      await api.post("/v1/auth/logout", undefined, { auth: false });
    } catch {
      // signing out locally is what matters
    }
    forget();
  }, [forget]);

  const updateUser = useCallback((patch: Partial<CloudUser>) => {
    setUser((u) => (u ? { ...u, ...patch } : u));
  }, []);

  const value = useMemo(
    () => ({ status, user, signIn, signOut, forget, restore, updateUser }),
    [status, user, signIn, signOut, forget, restore, updateUser]
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionState {
  const ctx = useContext(SessionContext);
  if (!ctx) throw new Error("useSession outside <SessionProvider>");
  return ctx;
}

export const isServiceAdmin = (u: CloudUser | null) => u?.role === "service_admin";
