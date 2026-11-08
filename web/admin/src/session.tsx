// The signed-in staff account (story 50.33 §2, §4, §5). On start the console tries
// POST /v1/staff/refresh (the httpOnly cookie) to restore a session; the access token itself
// stays in api.ts's memory. The provider also keeps the session alive and honest:
//
//   - it refreshes the 10-minute token a minute before it runs out;
//   - it counts the time since the last admin request: at 13 minutes it warns, and at 15 it asks
//     the server, which ends an idle session (the sign-in screen then says why);
//   - it opens the re-auth dialog when an admin call answers 401 reauth_required, and the call
//     is sent again once the code is accepted;
//   - it keeps the last account-or-network refusal (403) for the frame to show.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  endSession,
  refreshStaff,
  setStaffHooks,
  type EndReason,
  type StaffAccount,
  type StaffSession,
} from "./api";
import { ReauthDialog } from "./pages/Reauth";

export type StaffStatus = "loading" | "signed_in" | "signed_out";

/** The idle rule (§4): the server ends a session 15 minutes after its last admin request. */
export const IDLE_WARN_MS = 13 * 60 * 1000;
export const IDLE_END_MS = 15 * 60 * 1000;

interface StaffState {
  status: StaffStatus;
  staff: StaffAccount | null;
  /** Why the server ended the last session (shown on the sign-in screen). */
  ended: EndReason | null;
  /** The last 403 an admin call got for the account or the network. */
  denied: string | null;
  clearDenied: () => void;
  /** True from 13 idle minutes until the next admin request. */
  idleWarning: boolean;
  /** POST /v1/staff/logout and forget the session. */
  signOut: () => Promise<void>;
  /** POST /v1/staff/sessions/revoke-all: every session of the account ends, this one too. */
  signOutEverywhere: () => Promise<void>;
}

const StaffContext = createContext<StaffState | undefined>(undefined);

export function StaffSessionProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<StaffStatus>("loading");
  const [staff, setStaff] = useState<StaffAccount | null>(null);
  const [ended, setEnded] = useState<EndReason | null>(null);
  const [denied, setDenied] = useState<string | null>(null);
  const [idleWarning, setIdleWarning] = useState(false);
  const [reauthOpen, setReauthOpen] = useState(false);

  const statusRef = useRef<StaffStatus>("loading");
  const refreshTimer = useRef<ReturnType<typeof setTimeout>>();
  const warnTimer = useRef<ReturnType<typeof setTimeout>>();
  const endTimer = useRef<ReturnType<typeof setTimeout>>();
  const reauth = useRef<{ promise: Promise<boolean>; resolve: (ok: boolean) => void } | null>(null);

  const stopIdle = useCallback(() => {
    clearTimeout(warnTimer.current);
    clearTimeout(endTimer.current);
  }, []);

  // the idle clock restarts on each admin request (and on sign-in)
  const restartIdle = useCallback(() => {
    stopIdle();
    setIdleWarning(false);
    warnTimer.current = setTimeout(() => setIdleWarning(true), IDLE_WARN_MS);
    endTimer.current = setTimeout(() => {
      // the server decides: an idle session's refresh is refused with reason "idle"; one kept
      // alive elsewhere (another tab) comes back and the clock starts again
      void refreshStaff().then((r) => {
        if (r !== null && typeof r === "object") restartIdle();
      });
    }, IDLE_END_MS);
  }, [stopIdle]);

  const onSession = useCallback(
    (s: StaffSession | null, reason?: EndReason) => {
      clearTimeout(refreshTimer.current);
      if (s) {
        setStaff(s.staff);
        setEnded(null);
        if (statusRef.current !== "signed_in") restartIdle();
        statusRef.current = "signed_in";
        setStatus("signed_in");
        // renew a minute before the token runs out
        const ms = Math.max(30, s.expires_in - 60) * 1000;
        refreshTimer.current = setTimeout(() => void refreshStaff(), ms);
      } else {
        stopIdle();
        setIdleWarning(false);
        setStaff(null);
        setDenied(null);
        setEnded(reason ?? null);
        statusRef.current = "signed_out";
        setStatus("signed_out");
        reauth.current?.resolve(false);
      }
    },
    [restartIdle, stopIdle]
  );

  const onReauth = useCallback(() => {
    if (!reauth.current) {
      let resolve: (ok: boolean) => void = () => {};
      const promise = new Promise<boolean>((r) => {
        resolve = r;
      });
      reauth.current = {
        promise,
        resolve: (ok) => {
          reauth.current = null;
          setReauthOpen(false);
          resolve(ok);
        },
      };
      setReauthOpen(true);
    }
    return reauth.current.promise;
  }, []);

  // the hooks go in before the first refresh below (effects run in order)
  useEffect(() => {
    setStaffHooks({
      onSession,
      onReauth,
      onActivity: restartIdle,
      onDenied: setDenied,
    });
    return () => setStaffHooks({});
  }, [onSession, onReauth, restartIdle]);

  useEffect(() => {
    let live = true;
    void refreshStaff().then((r) => {
      // a network failure tells no one: there is no session to show
      if (live && r === null && statusRef.current === "loading") {
        statusRef.current = "signed_out";
        setStatus("signed_out");
      }
    });
    return () => {
      live = false;
    };
  }, []);

  useEffect(
    () => () => {
      clearTimeout(refreshTimer.current);
      clearTimeout(warnTimer.current);
      clearTimeout(endTimer.current);
    },
    []
  );

  const signOut = useCallback(async () => {
    try {
      await api.post("/v1/staff/logout", undefined, { auth: false });
    } catch {
      // signing out here is what matters
    }
    endSession();
  }, []);

  const signOutEverywhere = useCallback(async () => {
    await api.post("/v1/staff/sessions/revoke-all");
    endSession();
  }, []);

  const clearDenied = useCallback(() => setDenied(null), []);

  const value = useMemo(
    () => ({
      status,
      staff,
      ended,
      denied,
      clearDenied,
      idleWarning,
      signOut,
      signOutEverywhere,
    }),
    [status, staff, ended, denied, clearDenied, idleWarning, signOut, signOutEverywhere]
  );
  return (
    <StaffContext.Provider value={value}>
      {children}
      <ReauthDialog open={reauthOpen} onDone={(ok) => reauth.current?.resolve(ok)} />
    </StaffContext.Provider>
  );
}

export function useStaff(): StaffState {
  const ctx = useContext(StaffContext);
  if (!ctx) throw new Error("useStaff outside <StaffSessionProvider>");
  return ctx;
}
