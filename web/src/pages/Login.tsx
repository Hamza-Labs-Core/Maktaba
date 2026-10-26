// Login page (Story 10.2 cookie flow).
//
// The sign-in form is the only way in, on every machine: a node checks it against the
// home's accounts (Epic 35). `?next=` names where to go once signed in; only a path on
// this site is followed, never an absolute URL.
//
// A node that cannot reach its home checks a sign-in against its own copy of the
// home's database (Epic 37): the page says the sign-in holds on this machine only.
// A node that cannot (no copy, or one too far behind) refuses it: the page then
// says sign-in waits for the home, and once the home answers it probes the
// session again, so a user a reload landed here goes back in.
import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useLocation, Link } from "react-router-dom";
import { useHomeUnreachable } from "../components/HomeOffline";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { ApiError, isHomeUnreachable } from "../lib/api";
import { safeRelativePath } from "../lib/navigation";

interface LocationState {
  from?: string;
  /**
   * a sign-out everywhere ended this machine's sessions only ("here"), or those and the
   * home was told without its sessions checked ("unchecked") (Settings)
   */
  signedOut?: "here" | "unchecked";
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

  const dest =
    safeRelativePath(new URLSearchParams(location.search).get("next")) ??
    safeRelativePath((location.state as LocationState | null)?.from) ??
    "/";
  const signedOut = (location.state as LocationState | null)?.signedOut;

  const leave = useCallback(() => {
    if (left.current) return;
    left.current = true;
    nav(dest, { replace: true });
  }, [dest, nav]);

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
      } else if (err instanceof ApiError && err.problem.code === "password_change_waits") {
        setError(t("login.passwordChangeWaits"));
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
        {homeOut && (
          <p className="mkt-login__hint" role="note">
            {t("homeOffline.signInHere")}
          </p>
        )}
        {signedOut === "here" && (
          <p className="mkt-login__hint" role="note">
            {t("login.signedOutHere")}
          </p>
        )}
        {signedOut === "unchecked" && (
          <p className="mkt-login__hint" role="note">
            {t("login.signedOutUnchecked")}
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
