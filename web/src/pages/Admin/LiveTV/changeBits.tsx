// Small pieces the Changes page and the change detail share (story 33.11): the state
// chip, the kinds, the source, the checks summary, user names, error texts and the
// visible-only poll.
import { useCallback, useEffect, useRef } from "react";
import { ApiError } from "../../../lib/api";
import { lookupMessage, useI18n } from "../../../lib/i18n";
import {
  checksSummary,
  type ChangeState,
  type TvChange,
  type VerifyReport,
} from "../../../lib/tvChanges";

/**
 * A message for a key built from server data (an event action, a check, a field): the
 * catalog's text when it has one, else the fallback (the raw name) - never a miss.
 */
export function useTk(): (key: string, fallback: string) => string {
  const { locale } = useI18n();
  return useCallback(
    (key: string, fallback: string) => lookupMessage(locale, key) ?? fallback,
    [locale]
  );
}

/** How often an open Changes page re-reads (story 33.11 AC5: poll 2 s). */
export const CHANGES_POLL_MS = 2000;

const TONE: Record<ChangeState, string> = {
  draft: "neutral",
  staged: "accent",
  verifying: "accent",
  ready: "warn",
  approved: "accent",
  swapping: "accent",
  live: "success",
  superseded: "neutral",
  failed: "error",
  cancelled: "neutral",
  rolled_back: "warn",
};

export function StateChip({ state }: { state: ChangeState }) {
  const tk = useTk();
  return (
    <span className={`mkt-tvx-chip mkt-tvx-chip--${TONE[state] ?? "neutral"}`} data-state={state}>
      {tk(`tvchanges.state.${state}`, String(state))}
    </span>
  );
}

export function Kinds({ kinds }: { kinds: string[] | null | undefined }) {
  const tk = useTk();
  if (!kinds?.length) return <span className="mkt-tvx-muted">—</span>;
  return (
    <span className="mkt-tvx-kinds">
      {kinds.map((k) => (
        <span key={k} className="mkt-tvx-kind">
          {tk(`tvchanges.kind.${k}`, k)}
        </span>
      ))}
    </span>
  );
}

export function sourceLabel(tk: (k: string, f: string) => string, c: TvChange): string {
  const k = c.source?.kind ?? "manual";
  return tk(`tvchanges.source.${k}`, k);
}

/** Who made it (the owner's rule: only a manual change waits for an approval). */
export function OriginTag({ change }: { change: TvChange }) {
  const { t } = useI18n();
  const origin = change.origin === "auto" ? "auto" : "manual";
  return (
    <span className={`mkt-tvx-origin mkt-tvx-origin--${origin}`} data-testid="change-origin">
      {t(`tvchanges.origin.${origin}`)}
    </span>
  );
}

/** "3 pass · 1 warn · 0 fail" (or "—" before the checks ran). */
export function ChecksSummary({ report }: { report: VerifyReport | undefined }) {
  const { t, n } = useI18n();
  const s = checksSummary(report);
  if (s.total === 0 && !report?.error) return <span className="mkt-tvx-muted">—</span>;
  return (
    <span className="mkt-tvx-checks" aria-label={t("tvchanges.checksAria", s)}>
      <span className="mkt-tvx-checks__pass">{t("tvchanges.checksPass", { n: n(s.pass) })}</span>
      <span className="mkt-tvx-checks__warn">{t("tvchanges.checksWarn", { n: n(s.warn) })}</span>
      <span className="mkt-tvx-checks__fail">{t("tvchanges.checksFail", { n: n(s.fail) })}</span>
    </span>
  );
}

export function userName(
  users: Record<string, string> | undefined,
  id: string | undefined | null,
  fallback: string
): string {
  if (!id) return fallback;
  return users?.[id] ?? id.slice(0, 8);
}

/** The localised text of a failed change action (409 reasons by their error code). */
export function actionError(
  t: (k: string, v?: Record<string, string | number>) => string,
  e: unknown
): string {
  if (e instanceof ApiError) {
    const p = e.problem as unknown as Record<string, unknown>;
    const code = typeof p.error === "string" ? p.error : "";
    if (code === "second_admin_required") return t("tvchanges.err.secondAdmin");
    if (code === "ack_param_change_required") return t("tvchanges.err.ackParam");
    if (code === "change_in_flight") return t("tvchanges.err.inFlight");
    if (e.status === 409) {
      const st = typeof p.state === "string" ? p.state : "";
      return st ? t("tvchanges.err.state", { state: t(`tvchanges.state.${st}`) }) : e.message;
    }
    return e.message;
  }
  return e instanceof Error ? e.message : String(e);
}

/**
 * Runs `fn` now and every `ms` while the page is visible (a hidden tab does not poll;
 * showing it again reads at once). `fn` is read through a ref so a new closure does not
 * restart the timer.
 */
export function useVisiblePoll(fn: () => void | Promise<void>, ms: number, enabled = true): void {
  const ref = useRef(fn);
  ref.current = fn;
  const tick = useCallback(() => void ref.current(), []);
  useEffect(() => {
    if (!enabled) return;
    let timer: number | null = null;
    const start = () => {
      if (timer === null) timer = window.setInterval(tick, ms);
    };
    const stop = () => {
      if (timer !== null) window.clearInterval(timer);
      timer = null;
    };
    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        tick();
        start();
      } else stop();
    };
    tick();
    if (document.visibilityState === "visible") start();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      stop();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [tick, ms, enabled]);
}

/** A time as a short local clock ("20:30") with the full date in its title. */
export function Clock({ at }: { at: string | null | undefined }) {
  const { locale, formatDate } = useI18n();
  if (!at) return <span className="mkt-tvx-muted">—</span>;
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return <span>{at}</span>;
  const clock = new Intl.DateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
    hour: "2-digit",
    minute: "2-digit",
  }).format(d);
  return (
    <time dateTime={at} title={formatDate(at)} className="mkt-tvx-clock">
      {clock}
    </time>
  );
}
