import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { createPortal } from "react-dom";
import { clsx } from "../internal/clsx";
import "./toast.css";

// Story 17.2 / 17.5 — Toast + ToastProvider + useToast.
//
// The provider hosts an aria-live region (polite for info/success,
// assertive for error) so screen readers announce transient messages.
// `useToast().show(...)` returns an id; identical (id-deduped) toasts
// are not stacked, matching the spec's idempotent-retry dedupe note.

export type ToastTone = "info" | "success" | "warn" | "error";

export interface ToastOptions {
  /** Stable id; re-showing the same id replaces instead of stacking. */
  id?: string;
  tone?: ToastTone;
  message: ReactNode;
  /** Auto-dismiss ms; 0 = sticky (errors default sticky). */
  durationMs?: number;
}

export interface ToastProps {
  tone: ToastTone;
  message: ReactNode;
  onDismiss: () => void;
}

interface ActiveToast extends Required<Omit<ToastOptions, "durationMs">> {
  durationMs: number;
}

interface ToastApi {
  show: (opts: ToastOptions) => string;
  dismiss: (id: string) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

let counter = 0;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ActiveToast[]>([]);
  const timers = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());

  const dismiss = useCallback((id: string) => {
    setToasts((cur) => cur.filter((t) => t.id !== id));
    const handle = timers.current.get(id);
    if (handle) {
      clearTimeout(handle);
      timers.current.delete(id);
    }
  }, []);

  const show = useCallback(
    (opts: ToastOptions) => {
      const id = opts.id ?? `toast-${++counter}`;
      const tone = opts.tone ?? "info";
      const durationMs = opts.durationMs ?? (tone === "error" ? 0 : 5000);
      setToasts((cur) => {
        const next = cur.filter((t) => t.id !== id);
        return [...next, { id, tone, message: opts.message, durationMs }];
      });
      const existing = timers.current.get(id);
      if (existing) clearTimeout(existing);
      if (durationMs > 0) {
        timers.current.set(
          id,
          setTimeout(() => dismiss(id), durationMs)
        );
      }
      return id;
    },
    [dismiss]
  );

  const api = useMemo<ToastApi>(() => ({ show, dismiss }), [show, dismiss]);

  return (
    <ToastContext.Provider value={api}>
      {children}
      {typeof document !== "undefined" &&
        createPortal(
          <div className="mk-toast-region">
            {toasts.map((t) => (
              <Toast key={t.id} tone={t.tone} message={t.message} onDismiss={() => dismiss(t.id)} />
            ))}
          </div>,
          document.body
        )}
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    throw new Error("useToast must be used within a <ToastProvider>");
  }
  return ctx;
}

export function Toast({ tone, message, onDismiss }: ToastProps) {
  // Each toast is its own live region: errors assertive, the rest
  // polite. No separate hidden mirror (avoids double announcements).
  return (
    <div
      className={clsx("mk-toast", `mk-toast--${tone}`)}
      role={tone === "error" ? "alert" : "status"}
      aria-live={tone === "error" ? "assertive" : "polite"}
    >
      <span className="mk-toast__message">{message}</span>
      <button type="button" className="mk-toast__close" aria-label="Dismiss" onClick={onDismiss}>
        <svg viewBox="0 0 16 16" width={14} height={14} aria-hidden="true">
          <path
            d="M4 4l8 8M12 4l-8 8"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
          />
        </svg>
      </button>
    </div>
  );
}
