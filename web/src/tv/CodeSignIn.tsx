// Epic 58 story 58.09 — signing a Samsung or LG TV in by the home's own code, inside the
// launcher (src/tv/session.ts says why it is not the cookie sign-in). The TV asks the home
// for a code (POST /api/pairing/device), shows it large with a QR code of the approve page,
// and polls (POST /api/pairing/device/token) until a person signed in on a phone or a
// computer approves it there. A password is never typed on the TV: over plain HTTP on the
// LAN that is the only safe way, and the screen says the connection is not encrypted.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { QRCode } from "../components/QRCode";
import { isTokenAnswer, type TvSession } from "./session";

interface DeviceStart {
  code: string;
  device_code: string;
  expires_at: string;
  verify_path: string;
  verify_url: string;
  interval: number;
}

type Phase = "asking" | "showing" | "error";

/** The approve page's address without the code (what the TV says to open). */
export function approvePage(start: DeviceStart, origin: string): string {
  const url = start.verify_url || `${origin}${start.verify_path}`;
  return url.replace(/\?.*$/, "");
}

export function CodeSignIn({
  session,
  deviceLabel,
  onSignedIn,
  plain,
}: {
  session: TvSession;
  deviceLabel: string;
  onSignedIn: () => void;
  plain: boolean;
}) {
  const { t, dir, locale, n } = useI18n();
  const [start, setStart] = useState<DeviceStart | null>(null);
  const [phase, setPhase] = useState<Phase>("asking");
  const [lookedUp, setLookedUp] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const alive = useRef(true);

  const stop = () => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
  };

  const ask = useCallback(async () => {
    stop();
    setPhase("asking");
    setLookedUp(false);
    try {
      const s = await api.post<DeviceStart>("/api/pairing/device", { device_label: deviceLabel });
      if (!alive.current) return;
      setStart(s);
      setPhase("showing");
      poll(s);
    } catch {
      if (!alive.current) return;
      setPhase("error");
    }
  }, [deviceLabel]);

  const poll = (s: DeviceStart) => {
    const every = Math.max(1, s.interval || 2) * 1000;
    timer.current = setTimeout(async () => {
      try {
        const res = await api.post<unknown>("/api/pairing/device/token", {
          device_code: s.device_code,
        });
        if (!alive.current) return;
        if (isTokenAnswer(res)) {
          session.save(res);
          onSignedIn();
          return;
        }
        if (res && typeof res === "object" && (res as { looked_up?: unknown }).looked_up === true)
          setLookedUp(true);
        poll(s);
      } catch (e) {
        if (!alive.current) return;
        // the code ran out, or the home restarted: a new one at once
        if (e instanceof ApiError && e.status === 410) void ask();
        else poll(s);
      }
    }, every);
  };

  useEffect(() => {
    alive.current = true;
    void ask();
    return () => {
      alive.current = false;
      stop();
    };
  }, [ask]);

  const minutes = start
    ? Math.max(1, Math.round((Date.parse(start.expires_at) - Date.now()) / 60000))
    : 10;
  const page = start ? approvePage(start, window.location.origin) : "";

  return (
    <main className="tv-signin tv-code" dir={dir} lang={locale} data-testid="tv-code-signin">
      <h1>{t("tv.code.title")}</h1>
      {phase === "error" && <p role="alert">{t("tv.code.failed")}</p>}
      {start && phase === "showing" && (
        <div className="tv-code__body">
          <div className="tv-code__text">
            <p className="tv-code__code" data-testid="tv-code" dir="ltr">
              {start.code}
            </p>
            <p>
              {t("tv.code.where", { url: "\u0000" })
                .split("\u0000")
                .flatMap((part, i) =>
                  i === 0
                    ? [part]
                    : [
                        <bdi key="u" dir="ltr">
                          {page}
                        </bdi>,
                        part,
                      ]
                )}
            </p>
            <p className="tv-code__quiet">{t("tv.code.expires", { n: n(minutes) })}</p>
            <p className="tv-code__quiet" aria-live="polite">
              {lookedUp ? t("tv.code.lookedUp") : t("tv.code.waiting")}
            </p>
          </div>
          <QRCode value={start.verify_url || page} label={t("tv.code.qr")} size={300} />
        </div>
      )}
      {plain && (
        <p className="tv-code__plain" data-testid="tv-plain">
          {t("tv.plainHttp")} · {t("tv.code.onlyCode")}
        </p>
      )}
      <button type="button" autoFocus onClick={() => void ask()} data-testid="tv-code-new">
        {t("tv.code.new")}
      </button>
    </main>
  );
}
