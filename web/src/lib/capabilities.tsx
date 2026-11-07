// What this home has turned on (Epic 51 story 51.11; the server is 51.09).
//
// GET /api/capabilities answers which of the user-facing features are on. The
// client, the same shape as lib/flags:
//
//   - starts with every feature on and stays so until the home answers: an app
//     never hides a feature because the home did not answer;
//   - refreshes at sign-in, when the window comes back to the front, every
//     5 minutes with If-None-Match (a 304 keeps the answer), and at once on
//     `capabilities_changed` from /ws/home;
//   - keeps the last good answer when a refresh fails; a home without the route
//     (404, an older home) is all on;
//   - ignores a feature it does not know (a newer home's) and treats one the
//     answer leaves out (an older home) as on;
//   - holds the answer in memory only.
//
// A viewer's app hides what is off; an admin's explains it with a link to
// Components. "Admin" means the answer has an `admin` block, so a cloud-linked
// play sign-in is a viewer even on an admin's account.
import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { useAuth } from "./auth";
import { useI18n } from "./i18n";
import { subscribe as subscribeServer } from "./ws";
import { CLIENT_HEADER, clientHeaderValue } from "./clientHeader";
import { SERVER_PAGES } from "./edition";
import { featureOffText } from "./featureWords";

/** Every feature this client knows (the catalog's `features`, 51.09's order). */
export const KNOWN_FEATURES = [
  "live_tv",
  "transcripts",
  "subtitles",
  "semantic_search",
  "chapters",
  "youtube",
  "plex",
  "remote_access",
  "recommendations",
] as const;
export type FeatureId = (typeof KNOWN_FEATURES)[number];
export type FeatureState = "on" | "off";

/** Why a feature is off, as the admin block says. */
export interface FeatureOff {
  components: string[];
  reason: string;
}

export interface Capabilities {
  /** false until the home has answered once */
  answered: boolean;
  etag: string | null;
  features: Readonly<Record<FeatureId, FeatureState>>;
  /**
   * Plugin panels (story 54.20): true only when the answer says `plugin_panels: "on"`. Unlike
   * the known features, a missing key is off, so the app never asks an older server for them.
   */
  panels: boolean;
  /** null for a viewer */
  admin: {
    manageComponents: boolean;
    off: Readonly<Partial<Record<FeatureId, FeatureOff>>>;
  } | null;
}

/** Where an admin turns a feature on. */
export const COMPONENTS_PATH = "/settings/components";

export const CAPABILITIES_TTL_MS = 5 * 60_000;
/** A foreground refresh closer than this to the last one is skipped. */
const FOREGROUND_MIN_GAP_MS = 5_000;

/** The build switch: `VITE_CAPABILITIES_GATE=off` answers all on, hiding nothing. */
export const CAPABILITIES_GATE =
  ((import.meta.env.VITE_CAPABILITIES_GATE as string | undefined) ?? "on") !== "off";

const known = new Set<string>(KNOWN_FEATURES);

function allOn(): Record<FeatureId, FeatureState> {
  const out = {} as Record<FeatureId, FeatureState>;
  for (const f of KNOWN_FEATURES) out[f] = "on";
  return out;
}

export const ALL_ON: Capabilities = Object.freeze({
  answered: false,
  etag: null,
  features: Object.freeze(allOn()),
  panels: false,
  admin: null,
});

/** The known features of an answer (a missing one is on); null when it is not an answer. */
export function readCapabilities(raw: unknown, etag: string | null = null): Capabilities | null {
  if (!raw || typeof raw !== "object") return null;
  const r = raw as { etag?: unknown; features?: unknown; admin?: unknown };
  if (!r.features || typeof r.features !== "object") return null;
  const features = allOn();
  for (const [k, v] of Object.entries(r.features as Record<string, unknown>)) {
    if (known.has(k) && v === "off") features[k as FeatureId] = "off";
  }
  let admin: Capabilities["admin"] = null;
  if (r.admin && typeof r.admin === "object") {
    const a = r.admin as { manage_components?: unknown; off?: unknown };
    const off: Partial<Record<FeatureId, FeatureOff>> = {};
    if (a.off && typeof a.off === "object") {
      for (const [k, v] of Object.entries(a.off as Record<string, unknown>)) {
        if (!known.has(k) || !v || typeof v !== "object") continue;
        const o = v as { components?: unknown; reason?: unknown };
        off[k as FeatureId] = {
          components: Array.isArray(o.components)
            ? o.components.filter((c): c is string => typeof c === "string")
            : [],
          reason: typeof o.reason === "string" ? o.reason : "",
        };
      }
    }
    admin = { manageComponents: a.manage_components === true, off };
  }
  const tag = typeof r.etag === "string" && r.etag !== "" ? r.etag : etag;
  const panels = (r.features as Record<string, unknown>).plugin_panels === "on";
  return { answered: true, etag: tag, features, panels, admin };
}

/** What one GET answered. */
export type CapabilitiesAnswer =
  | { status: "ok"; body: unknown; etag: string | null }
  | { status: "not_modified" }
  | { status: "absent" };

const BASE = (import.meta.env.VITE_API_BASE ?? "") as string;

/** GET /api/capabilities with If-None-Match (a 304 and an older home's 404 are answers). */
export async function fetchCapabilities(etag: string | null): Promise<CapabilitiesAnswer> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    [CLIENT_HEADER]: clientHeaderValue(),
  };
  if (etag) headers["If-None-Match"] = `"${etag}"`;
  const res = await fetch(`${BASE}/api/capabilities`, { headers, credentials: "include" });
  if (res.status === 304) return { status: "not_modified" };
  if (res.status === 404) return { status: "absent" };
  if (!res.ok) throw new Error(`capabilities: ${res.status}`);
  const header = res.headers.get("ETag");
  const tag = header ? header.replace(/^W\//, "").replace(/"/g, "") : null;
  return { status: "ok", body: await res.json(), etag: tag };
}

export class CapabilitiesClient {
  private caps: Capabilities = ALL_ON;
  private fetchedAt = -Infinity;
  private inflight: Promise<void> | null = null;
  private generation = 0;
  private readonly listeners = new Set<() => void>();

  constructor(
    private readonly load: (etag: string | null) => Promise<CapabilitiesAnswer> = fetchCapabilities,
    private readonly now: () => number = () => Date.now(),
    private readonly gate: boolean = CAPABILITIES_GATE
  ) {}

  snapshot = (): Capabilities => this.caps;

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };

  /** Fetch again when the answer is older than `maxAgeMs` (0: always). */
  refresh(maxAgeMs = CAPABILITIES_TTL_MS): Promise<void> {
    if (!this.gate) return Promise.resolve();
    if (this.now() - this.fetchedAt < maxAgeMs) return Promise.resolve();
    if (this.inflight) return this.inflight;
    const gen = this.generation;
    const p: Promise<void> = this.load(this.caps.etag)
      .then((a) => {
        if (gen !== this.generation) return; // signed out meanwhile
        if (a.status === "not_modified") {
          this.fetchedAt = this.now();
          return;
        }
        const next =
          a.status === "absent" ? { ...ALL_ON, answered: true } : readCapabilities(a.body, a.etag);
        if (!next) return; // not an answer: keep the last good one
        this.fetchedAt = this.now();
        this.set(next);
      })
      .catch(() => {
        // keep the last good answer; try again at the next refresh
      })
      .finally(() => {
        if (this.inflight === p) this.inflight = null;
      });
    this.inflight = p;
    return p;
  }

  /** A `capabilities_changed` push: fetch now unless the answer already has that ETag. */
  changed(etag: unknown): Promise<void> {
    if (typeof etag === "string" && etag !== "" && etag === this.caps.etag) {
      return Promise.resolve();
    }
    return this.refresh(0);
  }

  /** Back to all on (sign-out, another user). */
  clear(): void {
    this.generation++;
    this.fetchedAt = -Infinity;
    this.inflight = null;
    this.set(ALL_ON);
  }

  private set(next: Capabilities): void {
    this.caps = next;
    this.listeners.forEach((l) => l());
  }
}

const CapabilitiesContext = createContext<CapabilitiesClient | null>(null);

/** Loads what the home has on for the signed-in user and keeps it fresh. */
export function CapabilitiesProvider({
  children,
  client: given,
}: {
  children: ReactNode;
  client?: CapabilitiesClient;
}) {
  const client = useMemo(() => given ?? new CapabilitiesClient(), [given]);
  const { user } = useAuth();
  const userId = user?.id ?? null;

  useEffect(() => {
    client.clear();
    if (!userId || !CAPABILITIES_GATE) return;
    void client.refresh(0);
    const timer = window.setInterval(() => void client.refresh(), CAPABILITIES_TTL_MS);
    const foreground = () => {
      if (document.visibilityState !== "hidden") void client.refresh(FOREGROUND_MIN_GAP_MS);
    };
    window.addEventListener("focus", foreground);
    document.addEventListener("visibilitychange", foreground);
    const off = subscribeServer("home", (ev) => {
      if (ev.type === "capabilities_changed") void client.changed(ev.payload?.etag);
    });
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", foreground);
      document.removeEventListener("visibilitychange", foreground);
      off();
    };
  }, [client, userId]);

  return <CapabilitiesContext.Provider value={client}>{children}</CapabilitiesContext.Provider>;
}

const subscribeNone = () => () => {};
const snapshotNone = (): Capabilities => ALL_ON;

/** The current answer (all on outside the provider and before the home answers). */
export function useCapabilities(): Capabilities {
  const client = useContext(CapabilitiesContext);
  return useSyncExternalStore(
    client ? client.subscribe : subscribeNone,
    client ? client.snapshot : snapshotNone
  );
}

/** Whether `id` is on on this home. */
export function useFeature(id: FeatureId): FeatureState {
  return useCapabilities().features[id];
}

/** Why `id` is off, for an admin; null while it is on or for a viewer. */
export function useFeatureAdmin(id: FeatureId): FeatureOff | null {
  const caps = useCapabilities();
  if (caps.features[id] === "on" || !caps.admin) return null;
  return caps.admin.off[id] ?? { components: [], reason: "" };
}

/** What a gate does with `id`: show it, explain it (an admin) or hide it (a viewer). */
export function useFeatureGate(id: FeatureId): "show" | "explain" | "hide" {
  const caps = useCapabilities();
  if (caps.features[id] === "on") return "show";
  return caps.admin ? "explain" : "hide";
}

/**
 * The gate a screen keeps while it is open: a feature that goes off while the
 * screen shows does not tear it down (its next call answers 409 component_off).
 * The first answer still counts, so a deep link opened before it is gated too.
 * Remount (a `key`) to decide again.
 */
export function useOpenFeatureGate(id: FeatureId): "show" | "explain" | "hide" {
  const caps = useCapabilities();
  const gate = useFeatureGate(id);
  const [kept, setKept] = useState<"show" | "explain" | "hide" | null>(null);
  useEffect(() => {
    if (kept === null && caps.answered) setKept(gate);
  }, [kept, caps.answered, gate]);
  return kept ?? gate;
}

/**
 * The admin's note for a feature that is off: "Live TV is off on this home." (lib/featureWords)
 * with a link to Components (where the edition has that page).
 */
export function FeatureOffNote({ id, className }: { id: FeatureId; className?: string }) {
  const { t } = useI18n();
  return (
    <div
      className={`mkt-feature-off${className ? ` ${className}` : ""}`}
      role="note"
      data-feature-off={id}
    >
      <p>{featureOffText(id, t)}</p>
      {SERVER_PAGES && (
        <Link className="mkt-btn mkt-btn--secondary" to={COMPONENTS_PATH}>
          {t("capabilities.turnOn")}
        </Link>
      )}
    </div>
  );
}

/**
 * Renders its children while `id` is on; off, the admin's note (or `fallback`)
 * for an admin and nothing for a viewer.
 */
export function Feature({
  id,
  fallback,
  children,
}: {
  id: FeatureId;
  fallback?: ReactNode;
  children: ReactNode;
}) {
  const gate = useFeatureGate(id);
  if (gate === "show") return <>{children}</>;
  if (gate === "hide") return null;
  return <>{fallback ?? <FeatureOffNote id={id} />}</>;
}
