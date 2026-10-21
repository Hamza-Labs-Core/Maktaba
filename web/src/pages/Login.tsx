// Login page (Story 10.2 cookie flow).
//
// Epic 35 (design §10.5): `?next=` names where to go once signed in. On the
// home it is the cluster sign-in step a node sent the user to
// (/api/cluster/sso/authorize?...), which is an API redirect: the page goes
// there with a full navigation. Any other relative path is an app route.
// Only a path on this site is followed, never an absolute URL. On a node, the
// cluster sign-in prompt offers to sign in through the home instead.
//
// A node that cannot reach its home checks a sign-in against its own copy of the
// home's database (Epic 37): the page says the sign-in holds on this machine only.
// A node that cannot (no copy, or one too far behind) refuses it: the page then
// says sign-in waits for the home, and once the home answers it probes the
// session again, so a user a reload landed here goes back in.
import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useLocation, Link } from "react-router-dom";
import { ClusterSignIn } from "../components/ClusterSignIn";
import { useHomeUnreachable } from "../components/HomeOffline";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { ApiError, apiUrl, isHomeUnreachable } from "../lib/api";
import { SSO_AUTHORIZE_PATH, assignLocation, safeRelativePath } from "../lib/navigation";

interface LocationState {
  from?: string;
}

export function Login() {
  const { login, user, refresh, signInAgain } = useAuth();
  const { t } = useI18n();
  const homeOut = useHomeUnreachable();
  const wasOut = useRef(false);
  // this machine refused a sign-in: it cannot check one until the home answers
  const [refused, setRefused] = useState(false);
  const nav = useNavigate();
  const location = useLocation();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const left = useRef(false);

  const next = safeRelativePath(new URLSearchParams(location.search).get("next"));
  const toAuthorize = next !== null && next.startsWith(SSO_AUTHORIZE_PATH);
  const dest =
    (toAuthorize ? null : next) ??
    safeRelativePath((location.state as LocationState | null)?.from) ??
    "/library";

  const leave = useCallback(() => {
    if (left.current) return;
    left.current = true;
    if (toAuthorize && next) assignLocation(apiUrl(next));
    else nav(dest, { replace: true });
  }, [toAuthorize, next, dest, nav]);

  // already signed in (or the session probe finished): go on
  useEffect(() => {
    if (user) leave();
  }, [user, leave]);

  // the home is back: a session it could not check may hold
  useEffect(() => {
    if (homeOut) wasOut.current = true;
    else if (wasOut.current) {
      wasOut.current = false;
      setRefused(false);
      void refresh();
    }
  }, [homeOut, refresh]);

  if (user) return null;

  if (homeOut && refused)
    return (
      <main className="mkt-login">
        <section className="mkt-login__form" aria-labelledby="mkt-login-offline">
          <h1 className="mkt-login__title">{t("app.title")}</h1>
          <h2 id="mkt-login-offline">{t("homeOffline.signInTitle")}</h2>
          <p>{t("homeOffline.signIn")}</p>
        </section>
      </main>
    );

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await login(username, password);
      leave();
    } catch (err) {
      if (isHomeUnreachable(err)) {
        setRefused(true);
      } else if (err instanceof ApiError && err.status === 401) {
        setError(t("login.error"));
      } else {
        setError(t("common.error"));
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="mkt-login">
      <form className="mkt-login__form" onSubmit={onSubmit} aria-label={t("login.submit")}>
        <h1 className="mkt-login__title">{t("app.title")}</h1>
        {!toAuthorize && !homeOut && <ClusterSignIn returnTo={dest} />}
        {homeOut && (
          <p className="mkt-login__hint" role="note">
            {t("homeOffline.signInHere")}
          </p>
        )}
        {!homeOut && signInAgain && (
          <p className="mkt-login__hint" role="note">
            {t("login.signInAgain")}
          </p>
        )}
        <label className="mkt-field">
          <span className="mkt-field__label">{t("login.username")}</span>
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
            autoFocus
          />
        </label>
        <label className="mkt-field">
          <span className="mkt-field__label">{t("login.password")}</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>
        {error && (
          <div className="mkt-alert" role="alert">
            {error}
          </div>
        )}
        <button type="submit" className="mkt-btn mkt-btn--primary" disabled={submitting}>
          {submitting ? t("common.loading") : t("login.submit")}
        </button>
        <div className="mkt-login__links">
          <Link to="/register" className="mkt-login__link">
            {t("login.register")}
          </Link>
        </div>
      </form>
    </main>
  );
}
