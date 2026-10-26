// Shared bits of the cloud pages: loading data, turning API errors into copy,
// and the page frame.
import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { CloudApiError } from "./api";
import { useI18n, type I18nState } from "./i18n";

/** The copy for an API error: the catalog's `error.<code>`, else the server's message. */
export function errorMessage(t: I18nState["t"], has: I18nState["has"], err: unknown): string {
  if (err instanceof CloudApiError) {
    if (err.code === "too_many_attempts" && err.retryAfter) {
      return t("error.too_many_attempts_wait", { minutes: Math.ceil(err.retryAfter / 60) });
    }
    if (has(`error.${err.code}`)) return t(`error.${err.code}`);
    if (err.message && err.message !== err.code) return err.message;
    return t("error.generic_code", { code: err.code });
  }
  return t("error.network");
}

export function useErrorMessage() {
  const { t, has } = useI18n();
  return useCallback((err: unknown) => errorMessage(t, has, err), [t, has]);
}

export interface Loaded<T> {
  data: T | null;
  error: unknown;
  loading: boolean;
  reload: () => void;
}

/** Runs `fn` on mount and whenever `key` changes; the latest call wins. */
export function useLoad<T>(fn: () => Promise<T>, key: string): Loaded<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  useEffect(() => {
    let live = true;
    setLoading(true);
    setError(null);
    fnRef
      .current()
      .then((d) => {
        if (live) setData(d);
      })
      .catch((e: unknown) => {
        if (live) setError(e);
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [key, tick]);

  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { data, error, loading, reload };
}

/** The failed-load state: "Access denied" for a 403, else an error with Retry. */
export function LoadError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useI18n();
  const message = useErrorMessage();
  if (error instanceof CloudApiError && error.status === 403) {
    return (
      <ErrorState
        kind="permission"
        title={t("admin.denied.title")}
        description={t("admin.denied.body")}
      />
    );
  }
  return (
    <ErrorState
      kind={error instanceof CloudApiError ? "server" : "network"}
      title={t("common.loadFailed")}
      description={message(error)}
      action={
        onRetry ? (
          <Button variant="secondary" onClick={onRetry}>
            {t("common.retry")}
          </Button>
        ) : undefined
      }
    />
  );
}

export function Page({
  title,
  actions,
  children,
}: {
  title: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="mkc-page">
      <header className="mkc-page__header">
        <h1>{title}</h1>
        {actions && <div className="mkc-row">{actions}</div>}
      </header>
      {children}
    </section>
  );
}

export function Loading() {
  const { t } = useI18n();
  return (
    <p className="mkc-muted" role="status">
      {t("common.loading")}
    </p>
  );
}

/** A banner line (success / info / warn / error) using the design tokens. */
export function Banner({
  tone,
  children,
}: {
  tone: "success" | "info" | "warn" | "error";
  children: ReactNode;
}) {
  return (
    <div className={`mkc-banner mkc-banner--${tone}`} role={tone === "error" ? "alert" : "status"}>
      {children}
    </div>
  );
}

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

export function useBytes() {
  const { n } = useI18n();
  return useCallback(
    (bytes: number) => {
      let v = bytes;
      let i = 0;
      while (Math.abs(v) >= 1000 && i < UNITS.length - 1) {
        v /= 1000;
        i++;
      }
      return `${n(v, { maximumFractionDigits: i === 0 ? 0 : 1 })} ${UNITS[i]}`;
    },
    [n]
  );
}

export function useMoney() {
  const { n } = useI18n();
  return useCallback(
    (cents: number, currency = "usd") =>
      n(cents / 100, { style: "currency", currency: currency.toUpperCase() }),
    [n]
  );
}

/** Password policy from the contract: 10–256 characters, no edge whitespace. */
export function passwordProblem(pw: string): "short" | "long" | "space" | null {
  if (pw.length < 10) return "short";
  if (pw.length > 256) return "long";
  if (pw.trim() !== pw) return "space";
  return null;
}
