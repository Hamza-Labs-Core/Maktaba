// Login page (Story 10.2 cookie flow).
//
// Epic 35 (design §10.5): `?next=` names where to go once signed in. On the
// home it is the cluster sign-in step a node sent the user to
// (/api/cluster/sso/authorize?...), which is an API redirect: the page goes
// there with a full navigation. Any other relative path is an app route.
// Only a path on this site is followed, never an absolute URL. On a node, the
// cluster sign-in prompt offers to sign in through the home instead.
import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useLocation, Link } from "react-router-dom";
import { ClusterSignIn } from "../components/ClusterSignIn";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { ApiError, apiUrl } from "../lib/api";
import { SSO_AUTHORIZE_PATH, assignLocation, safeRelativePath } from "../lib/navigation";

interface LocationState {
  from?: string;
}

export function Login() {
  const { login, user } = useAuth();
  const { t } = useI18n();
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

  if (user) return null;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await login(username, password);
      leave();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
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
        {!toAuthorize && <ClusterSignIn returnTo={dest} />}
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
