// Epic 57: what the signed-in account is under parental controls, for the pages.
//
// The home filters and refuses on its own; this only lets the web hide what the
// home would refuse and show the time the home says is left:
//
//   - `restricted`: the account is a restricted one (GET /api/me/parental). Its
//     pages show no link to what the home answers 403 `restricted` on.
//   - `children`: the restricted accounts this person looks after
//     (GET /api/parental/children; an admin: all of them). Not asked for a
//     restricted account, which looks after no one.
//   - `pendingRequests`: how many requests wait for this person's answer
//     (GET /api/parental/requests), asked only for someone who looks after a
//     restricted account. `refresh` reads everything again (after an answer).
//   - `time`: GET /api/me/parental/time, asked only for a restricted account
//     (only they have time rules) and only while a page shows it (useMyTime). The
//     web keeps no count of its own: every number shown is the home's last answer.
//     It is not asked while the tab is hidden, nor again after a refusal until the
//     instant the home named or the viewer's tap.
//
// Outside the provider, and until the home answers, nobody is restricted: the web
// never hides a page because the home did not answer (the home still refuses).
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
import { useOptionalAuth } from "./auth";
import {
  fetchChannelAccess,
  fetchChildren,
  fetchMyLimit,
  fetchMyTime,
  type Child,
  type MyTime,
} from "./parentalApi";
import { fetchRequests } from "./familyApi";
import { ASKED_TIME, forgetAsked } from "./parentalAsked";

export interface MyParental {
  /** false until the home has answered once */
  answered: boolean;
  restricted: boolean;
  /** the tier limit of this sign-in (account or device); null: none */
  maxTier: number | null;
  /** the limit this sign-in's device gave, when it gave one */
  deviceLimit: number | null;
  /** the device said something looser, which waits for a guardian or an admin */
  devicePending: boolean;
  children: Child[];
  /** the requests that wait for this person's answer */
  pendingRequests: number;
  /** asks the home again now */
  refresh: () => void;
}

export const NOT_RESTRICTED: MyParental = Object.freeze({
  answered: false,
  restricted: false,
  maxTier: null,
  deviceLimit: null,
  devicePending: false,
  children: [],
  pendingRequests: 0,
  refresh: () => {},
});

const ParentalContext = createContext<MyParental>(NOT_RESTRICTED);

/** A foreground refresh closer than this to the last one is skipped. */
const FOREGROUND_MIN_GAP_MS = 60_000;

/** Reads the account's limit at sign-in and when the window comes back. */
export function ParentalProvider({ children }: { children: ReactNode }) {
  const userId = useOptionalAuth()?.user?.id ?? null;
  const [state, setState] = useState<MyParental>(NOT_RESTRICTED);
  const loadRef = useRef<() => void>(() => {});
  const refresh = useCallback(() => loadRef.current(), []);

  useEffect(() => {
    setState(NOT_RESTRICTED);
    if (!userId) return;
    let alive = true;
    let last = 0;
    const load = () => {
      last = Date.now();
      void fetchMyLimit()
        .then(async (mine) => {
          // a restricted account looks after no one (the home answers an empty list)
          const kids = mine.restricted ? [] : await fetchChildren().catch(() => [] as Child[]);
          // only someone who looks after an account has requests to answer
          const waiting =
            kids.length > 0
              ? await fetchRequests()
                  .then((r) => r.length)
                  .catch(() => 0)
              : 0;
          if (alive)
            setState({
              answered: true,
              ...mine,
              children: kids,
              pendingRequests: waiting,
              refresh,
            });
        })
        .catch(() => {
          // an older home, or no answer: keep what is known
        });
    };
    load();
    loadRef.current = load;
    const foreground = () => {
      if (document.visibilityState === "hidden") return;
      if (Date.now() - last >= FOREGROUND_MIN_GAP_MS) load();
    };
    window.addEventListener("focus", foreground);
    document.addEventListener("visibilitychange", foreground);
    return () => {
      alive = false;
      loadRef.current = () => {};
      window.removeEventListener("focus", foreground);
      document.removeEventListener("visibilitychange", foreground);
    };
  }, [userId, refresh]);

  return <ParentalContext.Provider value={state}>{children}</ParentalContext.Provider>;
}

/** A fixed answer, for tests and stories. */
export function ParentalFixture({
  value,
  children,
}: {
  value: Partial<MyParental>;
  children: ReactNode;
}) {
  const v = useMemo(() => ({ ...NOT_RESTRICTED, answered: true, ...value }), [value]);
  return <ParentalContext.Provider value={v}>{children}</ParentalContext.Provider>;
}

export function useMyParental(): MyParental {
  return useContext(ParentalContext);
}

// ---- time ----

/** How often the home is asked while a page shows the time. */
export const TIME_POLL_MS = 30_000;
/** Closer to the end, sooner: the one-minute warning must not come late. */
export const TIME_POLL_NEAR_MS = 10_000;
/** "Near": this many seconds or fewer left. */
export const TIME_NEAR_S = 6 * 60;
/** The time left shows once this many seconds or fewer remain. */
export const TIME_SHOW_S = 15 * 60;
/** After a refusal, the home's own instant is asked about no sooner than this. */
export const TIME_REFUSED_MIN_MS = 5_000;
/** The warnings: five minutes and one minute before the end. */
export const TIME_WARNINGS_S = [300, 60] as const;

/**
 * The home's answer about the caller's time, asked again every TIME_POLL_MS while
 * `on` (and sooner near the end). null: not asked, or no answer yet. `ask` asks now.
 *
 * It does not ask when asking is no use:
 *   - while the tab is hidden: the question that came due is asked when the tab
 *     shows again;
 *   - after the home said watching is not allowed: the next question waits for
 *     the instant the home named (`next_allowed_at`) or for the viewer's own tap
 *     (`ask`), not for the clock.
 */
export function useMyTime(
  on: boolean,
  libraryId?: string
): { time: MyTime | null; ask: () => void } {
  const userId = useOptionalAuth()?.user?.id ?? null;
  const [time, setTime] = useState<MyTime | null>(null);
  const askRef = useRef<() => void>(() => {});

  useEffect(() => {
    setTime(null);
    if (!on) {
      askRef.current = () => {};
      return;
    }
    let alive = true;
    let timer: number | undefined;
    // a question came due while the tab was hidden
    let due = false;
    const hidden = () => document.visibilityState === "hidden";
    const ask = () => {
      window.clearTimeout(timer);
      due = false;
      void fetchMyTime(libraryId)
        .then((t) => {
          if (!alive) return;
          setTime(t);
          if (!t.allowed) {
            // refused: nothing changes until the home's own instant, or a parent
            // gives more time and the viewer taps Check again
            const at = t.nextAllowedAt ? Date.parse(t.nextAllowedAt) : NaN;
            if (Number.isFinite(at)) later(Math.max(TIME_REFUSED_MIN_MS, at - Date.now() + 1000));
            return;
          }
          // watching is allowed: a request for more time has been answered or is
          // not needed, so the next time's-up screen may ask again
          forgetAsked(userId, ASKED_TIME);
          const near = t.remainingSeconds !== null && t.remainingSeconds <= TIME_NEAR_S;
          later(near ? TIME_POLL_NEAR_MS : TIME_POLL_MS);
        })
        .catch(() => {
          // no answer: the last one stays; the home refuses the stream itself
          if (alive) later(TIME_POLL_MS);
        });
    };
    // asks again in `ms`, unless the tab is hidden then: the question waits for it
    const later = (ms: number) => {
      window.clearTimeout(timer);
      // setTimeout holds at most about 24 days
      timer = window.setTimeout(
        () => {
          if (hidden()) due = true;
          else ask();
        },
        Math.min(ms, 0x7fffffff)
      );
    };
    const shown = () => {
      if (alive && due && !hidden()) ask();
    };
    document.addEventListener("visibilitychange", shown);
    askRef.current = ask;
    ask();
    return () => {
      alive = false;
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", shown);
    };
  }, [on, libraryId, userId]);

  return useMemo(() => ({ time, ask: () => askRef.current() }), [time]);
}

/** Whether the time left is close enough to the end to show. */
export function showsTimeLeft(time: MyTime | null): time is MyTime & { remainingSeconds: number } {
  return (
    !!time &&
    time.allowed &&
    time.limited &&
    time.remainingSeconds !== null &&
    time.remainingSeconds <= TIME_SHOW_S
  );
}

/**
 * The warning line an answer is under: 300 once five minutes or fewer remain, 60
 * once one minute or fewer; null above both, and when no rule covers now.
 */
export function warningLine(time: MyTime | null): number | null {
  if (!time || !time.allowed || !time.limited || time.remainingSeconds === null) return null;
  const left = time.remainingSeconds;
  return [...TIME_WARNINGS_S].reverse().find((w) => left <= w) ?? null;
}

// ---- live TV ----

/** The home's answer for one channel: may what it plays now be watched. */
export interface ChannelGate {
  /** the channel the answer is for; null: not asked, or no answer yet */
  channelId: string | null;
  allowed: boolean;
  /** when the answer may change (the programme's end) */
  until: string | null;
}

const NO_GATE: ChannelGate = Object.freeze({ channelId: null, allowed: true, until: null });
/** Never ask the home sooner than this after an answer. */
export const ACCESS_MIN_MS = 5_000;
/** With no `until`, a refusal is asked about again this often. */
export const ACCESS_RETRY_MS = 60_000;

/**
 * GET /api/channels/{id}/access for `channelId` (null: nothing is asked), asked
 * again when the home said the answer may change. A failed call answers "allowed":
 * the web never blocks on its own, and the home refuses the stream itself.
 */
export function useChannelAccess(channelId: string | null): ChannelGate {
  const [gate, setGate] = useState<ChannelGate>(NO_GATE);
  useEffect(() => {
    setGate(NO_GATE);
    if (!channelId) return;
    let alive = true;
    let timer: number | undefined;
    const ask = () => {
      void fetchChannelAccess(channelId)
        .then((a) => {
          if (!alive) return;
          setGate({ channelId, allowed: a.allowed, until: a.until });
          const at = a.until ? Date.parse(a.until) : NaN;
          const wait = Number.isFinite(at)
            ? Math.max(ACCESS_MIN_MS, at - Date.now() + 1000)
            : a.allowed
              ? null
              : ACCESS_RETRY_MS;
          // setTimeout holds at most about 24 days; a programme ends sooner
          if (wait !== null) timer = window.setTimeout(ask, Math.min(wait, 0x7fffffff));
        })
        .catch(() => {
          if (alive) setGate({ channelId, allowed: true, until: null });
        });
    };
    ask();
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [channelId]);
  return gate;
}
