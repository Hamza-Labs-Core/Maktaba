// The bot check on the web auth pages (Epic 50 story 50.30 §4): a Cloudflare Turnstile widget
// in managed mode, rendered only when the cloud sends its site key (/v1/auth/providers); its
// token goes with the form as turnstile_token, and the API verifies it. Native apps have no
// widget and send no token. The widget follows the page's language ("ar" in Arabic).
import { useEffect, useRef } from "react";
import { useI18n } from "./i18n";

const SCRIPT = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";

interface TurnstileApi {
  render: (el: HTMLElement, opts: Record<string, unknown>) => string;
  reset: (id: string) => void;
  remove: (id: string) => void;
}
declare global {
  interface Window {
    turnstile?: TurnstileApi;
  }
}

let loading: Promise<TurnstileApi> | null = null;

/** Loads Turnstile's script once. */
export function loadTurnstile(doc: Document = document): Promise<TurnstileApi> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  loading ??= new Promise((resolve, reject) => {
    const s = doc.createElement("script");
    s.src = SCRIPT;
    s.async = true;
    s.onload = () =>
      window.turnstile ? resolve(window.turnstile) : reject(new Error("turnstile"));
    s.onerror = () => {
      loading = null;
      reject(new Error("turnstile"));
    };
    doc.head.appendChild(s);
  });
  return loading;
}

/**
 * The widget. `onToken` gets each new token ("" when it expires or errors); `resetKey` changing
 * asks for a fresh one (a token is good once, so the form resets it after each submit).
 */
export function Turnstile({
  siteKey,
  onToken,
  resetKey,
}: {
  siteKey: string;
  onToken: (token: string) => void;
  resetKey?: number;
}) {
  const { t, locale } = useI18n();
  const box = useRef<HTMLDivElement>(null);
  const id = useRef<string | null>(null);
  const cb = useRef(onToken);
  cb.current = onToken;

  useEffect(() => {
    if (!siteKey || !box.current) return;
    let live = true;
    void loadTurnstile()
      .then((ts) => {
        if (!live || !box.current) return;
        id.current = ts.render(box.current, {
          sitekey: siteKey,
          language: locale === "ar" ? "ar" : "auto",
          callback: (tok: string) => cb.current(tok),
          "expired-callback": () => cb.current(""),
          "error-callback": () => cb.current(""),
        });
      })
      .catch(() => cb.current(""));
    return () => {
      live = false;
      if (id.current && window.turnstile) window.turnstile.remove(id.current);
      id.current = null;
    };
  }, [siteKey, locale]);

  useEffect(() => {
    if (resetKey && id.current && window.turnstile) {
      window.turnstile.reset(id.current);
      cb.current("");
    }
  }, [resetKey]);

  if (!siteKey) return null;
  return (
    <div ref={box} className="mkc-turnstile" role="group" aria-label={t("auth.turnstile.label")} />
  );
}
