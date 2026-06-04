// Login page (Story 10.2 cookie flow).
import { type FormEvent, useState } from "react";
import { useNavigate, useLocation, Link } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { ApiError } from "../lib/api";

interface LocationState {
  from?: string;
  reset?: boolean;
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

  if (user) {
    const dest = (location.state as LocationState | null)?.from ?? "/library";
    nav(dest, { replace: true });
    return null;
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await login(username, password);
      const dest = (location.state as LocationState | null)?.from ?? "/library";
      nav(dest, { replace: true });
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
        {(location.state as LocationState | null)?.reset && (
          <div className="mkt-alert mkt-alert--success" role="status">
            {t("reset.success")}
          </div>
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
          <Link to="/forgot-password" className="mkt-login__link">
            {t("login.forgot")}
          </Link>
          <Link to="/register" className="mkt-login__link">
            {t("login.register")}
          </Link>
        </div>
      </form>
    </main>
  );
}
