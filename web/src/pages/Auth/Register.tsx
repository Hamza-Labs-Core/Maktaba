// Self-service registration (web-pages-batch2).
//
// Real contract:
//   POST /api/auth/register { username, email, password }
//     → 200 { user }                  (cookie flow; user is signed in)
//     → 403 type=registration-closed  (open_registration off, users exist)
//     → 409 type=username-exists | email-exists
//
// There is no anonymous endpoint to probe whether registration is open,
// so the "registration closed" state is surfaced reactively from the
// 403 on submit. Styled like Login (outside the AppShell).
import { type FormEvent, useState } from "react";
import { useNavigate, Link } from "react-router-dom";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";

export function Register() {
  const { register, user } = useAuth();
  const { t } = useI18n();
  const nav = useNavigate();
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [closed, setClosed] = useState(false);

  if (user) {
    nav("/library", { replace: true });
    return null;
  }

  const mismatch = confirm !== "" && password !== confirm;
  const valid = username.trim() !== "" && email.trim() !== "" && password !== "" && !mismatch;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!valid) return;
    setSubmitting(true);
    setError(null);
    try {
      await register(username, email, password);
      nav("/library", { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.problem.type.includes("registration-closed")) {
          setClosed(true);
        } else if (err.status === 409) {
          setError(err.problem.detail || t("register.conflict"));
        } else {
          setError(err.problem.detail || t("common.error"));
        }
      } else {
        setError(t("common.error"));
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="mkt-login">
      <form className="mkt-login__form" onSubmit={onSubmit} aria-label={t("register.submit")}>
        <h1 className="mkt-login__title">{t("register.title")}</h1>

        {closed ? (
          <>
            <div className="mkt-alert" role="alert">
              {t("register.closed")}
            </div>
            <Link to="/login" className="mkt-login__link">
              {t("register.toLogin")}
            </Link>
          </>
        ) : (
          <>
            <label className="mkt-field">
              <span className="mkt-field__label">{t("register.username")}</span>
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
              <span className="mkt-field__label">{t("register.email")}</span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
                required
              />
            </label>
            <label className="mkt-field">
              <span className="mkt-field__label">{t("register.password")}</span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                required
                minLength={8}
              />
            </label>
            <label className="mkt-field">
              <span className="mkt-field__label">{t("register.confirm")}</span>
              <input
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
                required
              />
            </label>
            {mismatch && (
              <div className="mkt-alert" role="alert">
                {t("register.mismatch")}
              </div>
            )}
            {error && (
              <div className="mkt-alert" role="alert">
                {error}
              </div>
            )}
            <button
              type="submit"
              className="mkt-btn mkt-btn--primary"
              disabled={submitting || !valid}
            >
              {submitting ? t("common.loading") : t("register.submit")}
            </button>
            <Link to="/login" className="mkt-login__link">
              {t("register.haveAccount")}
            </Link>
          </>
        )}
      </form>
    </main>
  );
}
