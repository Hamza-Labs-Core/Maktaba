// /cloud-sign-in (Epic 42.9): Maktaba Cloud's web opens a connected home's player or remote
// here, on the home's relay address, with a one-use sign-in assertion in the fragment
// (#assertion=…), which no server sees. The page hands it to the home
// (POST /api/auth/cloud, the web bundle: the home answers with its own cookie session on this
// address), takes it out of the address bar, and opens the app. The home checks everything:
// the assertion (its signature, this home, one use, a minute), the link, the user; the rights
// are that home user's, capped by the link's scope.
import { useEffect, useRef, useState } from "react";
import { Navigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";

/** The assertion in this page's fragment (and takes the fragment out of the address). */
export function takeAssertion(): string | null {
  const m = /(?:^#|&)assertion=([^&]+)/.exec(window.location.hash);
  if (!m) return null;
  window.history.replaceState(window.history.state, "", window.location.pathname);
  return decodeURIComponent(m[1]);
}

export function CloudSignIn() {
  const { t } = useI18n();
  const { refresh, user } = useAuth();
  const [state, setState] = useState<"working" | "done" | "refused" | "missing">("working");
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const assertion = takeAssertion();
    if (!assertion) {
      setState("missing");
      return;
    }
    void (async () => {
      try {
        await api.post("/api/auth/cloud", { assertion });
        await refresh();
        setState("done");
      } catch (e) {
        console.warn("cloud sign-in refused", e instanceof ApiError ? e.status : e);
        setState("refused");
      }
    })();
  }, [refresh]);

  if (state === "done" && user) return <Navigate to="/" replace />;
  return (
    <section className="mkt-page mkt-login" aria-busy={state === "working"}>
      <h1>{t("cloudSignIn.title")}</h1>
      {state === "working" && (
        <p className="mkt-muted" role="status">
          {t("cloudSignIn.working")}
        </p>
      )}
      {(state === "refused" || state === "missing") && (
        <>
          <p className="mkt-alert" role="alert">
            {t(state === "refused" ? "cloudSignIn.refused" : "cloudSignIn.missing")}
          </p>
          <Button onClick={() => window.location.assign(`${import.meta.env.BASE_URL}login`)}>
            {t("cloudSignIn.password")}
          </Button>
        </>
      )}
    </section>
  );
}
