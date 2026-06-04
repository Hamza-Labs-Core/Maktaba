// Reset-password page (web-pages-batch2).
//
// Real contract:
//   POST /api/auth/reset-password { token, password }
//     → 204 No Content              (sessions+refresh revoked server-side)
//     → 400                         (token invalid/expired or weak password)
//
// The token arrives in the URL: /reset-password?token=<...>. On success
// the user is sent to /login to sign in with the new password. Styled
// like Login (outside the AppShell).
import { type FormEvent, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { api, ApiError } from "../../lib/api";

export function ResetPassword() {
  const { t } = useI18n();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mismatch = confirm !== "" && password !== confirm;
  const valid = token !== "" && password.length >= 8 && !mismatch;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!valid) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.post("/api/auth/reset-password", { token, password });
      nav("/login", { replace: true, state: { reset: true } });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.problem.detail || t("reset.invalid"));
      } else {
        setError(t("common.error"));
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="mkt-login">
      <form className="mkt-login__form" onSubmit={onSubmit} aria-label={t("reset.submit")}>
        <h1 className="mkt-login__title">{t("reset.title")}</h1>

        {token === "" ? (
          <>
            <div className="mkt-alert" role="alert">
              {t("reset.noToken")}
            </div>
            <Link to="/forgot-password" className="mkt-login__link">
              {t("reset.request")}
            </Link>
          </>
        ) : (
          <>
            <label className="mkt-field">
              <span className="mkt-field__label">{t("reset.newPassword")}</span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                required
                minLength={8}
                autoFocus
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
              {submitting ? t("common.loading") : t("reset.submit")}
            </button>
            <Link to="/login" className="mkt-login__link">
              {t("register.toLogin")}
            </Link>
          </>
        )}
      </form>
    </main>
  );
}
