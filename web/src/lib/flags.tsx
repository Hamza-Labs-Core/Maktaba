// Feature flags, client side (Epic 16 Story 16.6; the server is 16.8).
//
// GET /api/me/flags answers the signed-in user's resolved flags: the license's
// paid features, admin-only flags and beta cohorts, already merged by the
// server. The client:
//
//   - keeps the last good set for 60 s and refreshes when the window comes back
//     to the front (focus, visibilitychange) and every 60 s while it is open;
//   - keeps the last good set when a refresh fails (an outage never turns a
//     feature on or off by itself), and starts from all-off;
//   - ignores a flag it does not know (a newer server's) and a value that is not
//     a boolean;
//   - holds the set in memory only (nothing to tamper with on disk). The bundle
//     is signed with the server's Ed25519 identity for clients that keep it; the
//     web does not, and every gate is enforced by the server anyway.
//
// Gated UI is hidden, not disabled: `useFlag(key)` is false until the server
// says true, and `<Flag name>` renders nothing then.
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { api } from "./api";
import { useAuth } from "./auth";

/** Every flag this client knows (the server's api/internal/flags declarations). */
export const KNOWN_FLAGS = [
  "cloud_relay",
  "multi_user",
  "cloud_metadata_backup",
  "advanced_analytics",
  "federation",
  "flags_admin",
  "early_access",
] as const;
export type FlagKey = (typeof KNOWN_FLAGS)[number];
export type Flags = Readonly<Partial<Record<FlagKey, boolean>>>;

/** GET /api/me/flags. */
export interface FlagsBundle {
  flags: Record<string, unknown>;
  tier: string;
  kid: string;
  issued_at: string;
  expires_at: string;
  signature: string;
}

export const FLAGS_TTL_MS = 60_000;
/** A foreground refresh closer than this to the last one is skipped. */
const FOREGROUND_MIN_GAP_MS = 5_000;

const known = new Set<string>(KNOWN_FLAGS);

/** The known boolean flags of a bundle; null when the answer is not a bundle. */
export function readBundle(raw: unknown): Flags | null {
  if (!raw || typeof raw !== "object") return null;
  const b = raw as Partial<FlagsBundle>;
  if (
    typeof b.signature !== "string" ||
    !b.signature ||
    typeof b.kid !== "string" ||
    !b.flags ||
    typeof b.flags !== "object"
  ) {
    return null;
  }
  const out: Partial<Record<FlagKey, boolean>> = {};
  for (const [k, v] of Object.entries(b.flags)) {
    if (known.has(k) && typeof v === "boolean") out[k as FlagKey] = v;
  }
  return out;
}

const NONE: Flags = Object.freeze({});

export class FlagsClient {
  private flags: Flags = NONE;
  private fetchedAt = -Infinity;
  private inflight: Promise<void> | null = null;
  private generation = 0;
  private readonly listeners = new Set<() => void>();

  constructor(
    private readonly load: () => Promise<unknown> = () => api.get<FlagsBundle>("/api/me/flags"),
    private readonly now: () => number = () => Date.now()
  ) {}

  /** The current set (a new object after every change: a React snapshot). */
  snapshot = (): Flags => this.flags;

  isOn(key: FlagKey): boolean {
    return this.flags[key] === true;
  }

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };

  /** Fetch again when the set is older than `maxAgeMs` (0: always). */
  refresh(maxAgeMs = FLAGS_TTL_MS): Promise<void> {
    if (this.now() - this.fetchedAt < maxAgeMs) return Promise.resolve();
    if (this.inflight) return this.inflight;
    const gen = this.generation;
    const p: Promise<void> = this.load()
      .then((raw) => {
        const next = readBundle(raw);
        if (!next || gen !== this.generation) return; // refused, or signed out meanwhile
        this.fetchedAt = this.now();
        this.set(next);
      })
      .catch(() => {
        // keep the last good set; try again at the next refresh
      })
      .finally(() => {
        if (this.inflight === p) this.inflight = null;
      });
    this.inflight = p;
    return p;
  }

  /** Back to all-off (sign-out, another user). */
  clear(): void {
    this.generation++;
    this.fetchedAt = -Infinity;
    this.inflight = null;
    this.set(NONE);
  }

  private set(next: Flags): void {
    const same =
      Object.keys(next).length === Object.keys(this.flags).length &&
      KNOWN_FLAGS.every((k) => next[k] === this.flags[k]);
    if (same) return;
    this.flags = next;
    this.listeners.forEach((l) => l());
  }
}

const FlagsContext = createContext<FlagsClient | null>(null);

/**
 * Loads the signed-in user's flags and keeps them fresh. Without a user the set
 * is all-off.
 */
export function FlagsProvider({
  children,
  client: given,
}: {
  children: ReactNode;
  client?: FlagsClient;
}) {
  const client = useMemo(() => given ?? new FlagsClient(), [given]);
  const { user } = useAuth();
  const userId = user?.id ?? null;

  useEffect(() => {
    client.clear();
    if (!userId) return;
    void client.refresh(0);
    const timer = window.setInterval(() => void client.refresh(), FLAGS_TTL_MS);
    const foreground = () => {
      if (document.visibilityState !== "hidden") void client.refresh(FOREGROUND_MIN_GAP_MS);
    };
    window.addEventListener("focus", foreground);
    document.addEventListener("visibilitychange", foreground);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", foreground);
      document.removeEventListener("visibilitychange", foreground);
    };
  }, [client, userId]);

  return <FlagsContext.Provider value={client}>{children}</FlagsContext.Provider>;
}

/** The flags client (null outside FlagsProvider). */
export function useFlagsClient(): FlagsClient | null {
  return useContext(FlagsContext);
}

const subscribeNone = () => () => {};
const snapshotNone = (): Flags => NONE;

/** Every known flag's value (all-off outside FlagsProvider). */
export function useFlags(): Flags {
  const client = useContext(FlagsContext);
  return useSyncExternalStore(
    client ? client.subscribe : subscribeNone,
    client ? client.snapshot : snapshotNone
  );
}

/** Whether `key` is on for the signed-in user (false until the server says so). */
export function useFlag(key: FlagKey): boolean {
  return useFlags()[key] === true;
}

/** Renders its children only while `name` is on: gated UI is hidden, not disabled. */
export function Flag({ name, children }: { name: FlagKey; children: ReactNode }) {
  return useFlag(name) ? <>{children}</> : null;
}
