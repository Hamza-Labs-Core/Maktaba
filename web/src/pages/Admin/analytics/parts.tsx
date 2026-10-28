// Shared pieces of the analytics page (Epic 44 story 44.7): the loader every
// tab uses, the guards that turn a null or missing list into an empty one,
// and the small views (KPI tiles, labels for surfaces and apps).
import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Card } from "@ds/components/Card/Card";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ApiError } from "../../../lib/api";
import { useOptionalAuth } from "../../../lib/auth";
import { useI18n } from "../../../lib/i18n";
import { cachedAnswer, keepAnswer } from "../../../lib/pageCache";
import { LinesPlaceholder, Loading } from "../../../components/Placeholders";
import type { AnalyticsQuery, CountStat, Surface } from "../../../lib/analytics";

// The server (or a misrouted proxy) may send null lists, missing keys or
// not even an object; every card treats those as empty instead of crashing.
export const list = <T,>(v: unknown): T[] =>
  Array.isArray(v) ? (v.filter((x) => x != null) as T[]) : [];
export const num = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);
export const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};
export const str = (v: unknown): string => (typeof v === "string" ? v : "");

// TabProps is what every tab gets: the query (range and filters) and the
// refresh count (a click on Refresh loads the tab again).
export interface TabProps {
  q: AnalyticsQuery;
  nonce: number;
}

export interface Loaded<T> {
  data: T | null;
  error: unknown;
  loading: boolean;
}

// useLoad runs load whenever it (or nonce) changes. Only the latest run may
// write: a late answer for an old range never wins. A failed run clears the
// data (so a card never shows the old range's numbers), unless keepOnError
// (the live Now tab keeps what it last showed). With a `key` (what is read,
// and for which range and filters) the answer is kept for the user
// (lib/pageCache): a return, or going back to a range, shows it at once
// while the run reads it again.
export function useLoad<T>(
  load: () => Promise<T>,
  nonce: number | string,
  keepOnError = false,
  key?: string
): Loaded<T> {
  const user = useOptionalAuth()?.user?.id;
  const kept = key == null ? null : `admin:analytics:${key}`;
  const [state, setState] = useState<Loaded<T>>(() => ({
    data: kept ? (cachedAnswer<T>(user, kept) ?? null) : null,
    error: null,
    loading: true,
  }));
  useEffect(() => {
    let current = true;
    const start = kept ? cachedAnswer<T>(user, kept) : undefined;
    setState((s) => ({ ...s, data: start ?? s.data, loading: true }));
    load().then(
      (data) => {
        if (!current) return;
        if (kept) keepAnswer(user, kept, data);
        setState({ data, error: null, loading: false });
      },
      (error: unknown) => {
        if (current)
          setState((s) => ({ data: keepOnError ? s.data : null, error, loading: false }));
      }
    );
    return () => {
      current = false;
    };
    // `load` changes with what it reads, and `kept` with it
  }, [load, nonce, keepOnError, kept, user]);
  return state;
}

// licenseRefused reports whether the plan refused one of a tab's loads (Epic 16: a
// range over 30 days, the people, the apps): the tab then shows the reason alone.
export function licenseRefused(loads: Array<Loaded<unknown>>): boolean {
  return loads.some(
    (l) => l.error instanceof ApiError && (l.error.problem.type ?? "").includes("license-required")
  );
}

// LoadError shows the first failure of a tab's loads, in the API's words.
export function LoadError({ loads }: { loads: Array<Loaded<unknown>> }) {
  const { t } = useI18n();
  const failed = loads.find((l) => l.error);
  if (!failed) return null;
  const e = failed.error;
  return (
    <p className="mkt-error" role="alert">
      {e instanceof ApiError ? e.message : t("common.error")}
    </p>
  );
}

export function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <Card className="mkt-kpi">
      <span className="mkt-kpi__value">{value}</span>
      <span className="mkt-kpi__label">{label}</span>
    </Card>
  );
}

export function Kpis({ children }: { children: ReactNode }) {
  return <div className="mkt-analytics__kpis">{children}</div>;
}

// Panel is a titled card; while its data loads it says so, and when the
// data is empty it shows the empty message instead of its body.
export function Panel({
  title,
  loading,
  empty,
  emptyText,
  wide,
  children,
}: {
  title: string;
  loading?: boolean;
  empty?: boolean;
  emptyText?: string;
  /** the whole row of the grid (a table) */
  wide?: boolean;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <Card header={<h2>{title}</h2>} className={wide ? "mkt-analytics__wide" : undefined}>
      {loading ? (
        <Loading loading>
          <LinesPlaceholder />
        </Loading>
      ) : empty ? (
        <EmptyState kind="filtered_out" title={emptyText ?? t("analytics.empty")} />
      ) : (
        children
      )}
    </Card>
  );
}

// useLabels turns the server's codes into words: a surface, an app ("unknown"
// when the app did not say), and a dash for an empty value.
export function useLabels() {
  const { t } = useI18n();
  return {
    surface: (s: string) =>
      ["video", "app", "tuner", "remote"].includes(s) ? t(`analytics.surface.${s}`) : s,
    app: (a: string) =>
      !a || a === "unknown"
        ? t("analytics.app.unknown")
        : a === "plex"
          ? t("analytics.app.plex")
          : a,
    value: (v: string) => (!v || v === "unknown" ? t("analytics.unknown") : v),
    dash: (s: string | undefined | null) => (s ? s : "—"),
  };
}

// cleanStats drops null rows and fills missing numbers.
export function cleanStats(v: unknown): CountStat[] {
  return list<Record<string, unknown>>(v).map((s) => ({
    label: str(s.label),
    sessions: num(s.sessions),
    watch_sec: num(s.watch_sec),
    people: num(s.people),
  }));
}

export const surfaceKeys: Surface[] = ["video", "app", "tuner", "remote"];
