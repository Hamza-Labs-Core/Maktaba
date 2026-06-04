// Forgot-password request page (web-pages-batch2).
//
// Real contract:
//   POST /api/auth/forgot-password { email }  → 200 { ok: true } ALWAYS
//
// The server never reveals whether the address exists (anti-enumeration),
// so the UI always shows the same neutral confirmation after submit.
// Styled like Login (outside the AppShell).
import { type FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { api } from "../../lib/api";

export function ForgotPassword() {
  const { t } = useI18n();
  const [email, setEmail] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [sent, setSent] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    try {
      await api.post("/api/auth/forgot-password", { email });
    } catch {
      // The endpoint is best-effort + always-200; even a transport error
      // shouldn't leak signal, so we still show the neutral confirmation.
    } finally {
      setSubmitting(false);
      setSent(true);
    }
  }

  return (
    <main className="mkt-login">
      <form className="mkt-login__form" onSubmit={onSubmit} aria-label={t("forgot.submit")}>
        <h1 className="mkt-login__title">{t("forgot.title")}</h1>

        {sent ? (
          <>
            <p className="mkt-login__hint">{t("forgot.sent")}</p>
            <Link to="/login" className="mkt-login__link">
              {t("register.toLogin")}
            </Link>
          </>
        ) : (
          <>
            <p className="mkt-login__hint">{t("forgot.intro")}</p>
            <label className="mkt-field">
              <span className="mkt-field__label">{t("register.email")}</span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
                required
                autoFocus
              />
            </label>
            <button
              type="submit"
              className="mkt-btn mkt-btn--primary"
              disabled={submitting || email.trim() === ""}
            >
              {submitting ? t("common.loading") : t("forgot.submit")}
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
