// Epic 58 story 58.09's second part — signing a Samsung or LG TV in with a cloud code's grant.
//
// The launcher's first screen is the cloud code (story 58.03). Once a person approves it, the
// launcher loads this home's /tv/ and hands it the home's single-use grant over the bridge
// (`maktaba:signIn`, bigscreen/input/bridge.ts). /tv/ redeems it here, in the home's own
// origin (POST /api/pairing/device/token, as the home's own code is redeemed), and keeps the
// tokens in the home's origin (session.ts): no token crosses the bridge, and the grant is
// never put in a URL or a log. A cloud code's TV is restricted on the home: it never acts as
// an admin, whoever approved it (the home decides, principal.ApplyRestricted).
//
// The --bundle-ui copy of /tv/ (a packaged page, reaching the home cross-origin) can redeem a
// grant too (the home answers a packaged origin on the token route before a token), but not
// start the home's own code (a fresh code read cross-origin could be phished): with no grant
// it sends the person back to the launcher's code screen.
import { useEffect, useRef } from "react";
import { api } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { takeLauncherGrant, tellLauncher } from "../bigscreen/input/bridge";
import { isTokenAnswer, type TvSession } from "./session";

/** Redeems the launcher's grant; onFailed when there is none or the home refuses it. */
export function GrantSignIn({
  session,
  deviceLabel,
  onSignedIn,
  onFailed,
}: {
  session: TvSession;
  deviceLabel: string;
  onSignedIn: () => void;
  onFailed: () => void;
}) {
  const { t, dir, locale } = useI18n();
  // the grant is taken once: the run survives a re-render (and React's dev double mount)
  const once = useRef(false);
  const alive = useRef(true);
  const done = useRef({ onSignedIn, onFailed });
  done.current = { onSignedIn, onFailed };
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    if (once.current) return;
    once.current = true;
    void (async () => {
      const grant = await takeLauncherGrant();
      if (grant) {
        try {
          const res = await api.post<unknown>("/api/pairing/device/token", {
            device_code: grant,
            device_label: deviceLabel,
          });
          if (isTokenAnswer(res)) {
            session.save(res);
            if (alive.current) done.current.onSignedIn();
            return;
          }
        } catch {
          // refused (used, expired, a home restarted): the person signs in again
        }
      }
      if (alive.current) done.current.onFailed();
    })();
  }, [deviceLabel, session]);
  return (
    <main className="tv-signin tv-code" dir={dir} lang={locale} data-testid="tv-grant-signin">
      <h1>{t("tv.code.title")}</h1>
      <p aria-live="polite" aria-busy="true">
        {t("tv.code.signingIn")}
      </p>
    </main>
  );
}

/** The bundled copy with no grant: the code is on the launcher's start screen. */
export function BundledSignIn({ failed }: { failed: boolean }) {
  const { t, dir, locale } = useI18n();
  return (
    <main className="tv-signin tv-code" dir={dir} lang={locale} data-testid="tv-bundled-signin">
      <h1>{t("tv.code.title")}</h1>
      {failed && <p role="alert">{t("tv.code.grantFailed")}</p>}
      <p>{t("tv.code.bundled")}</p>
      <button
        type="button"
        autoFocus
        onClick={() => tellLauncher("maktaba:openHomePicker")}
        data-testid="tv-bundled-go"
      >
        {t("tv.code.bundledGo")}
      </button>
    </main>
  );
}
