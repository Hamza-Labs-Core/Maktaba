// Required password change (admin reset).
//
// Maktaba has no mailer, so a forgotten password is reset by an admin on
// the Users page. A temporary password set there must be replaced at the
// next sign-in: until then the server answers every other API call with
// 403 password-change-required, and RequireAuth shows only this form.
//
// Real contract:
//   POST /api/me/change-password { current_password, new_password } → 204
//     → 401 (the temporary password is wrong), 400 (too short / unchanged)
import { type FormEvent, useState } from "react";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { api, ApiError } from "../../lib/api";

const MIN_PASSWORD = 8;

export function ChangePasswordRequired() {
  const { t } = useI18n();
  const { passwordChanged, logout } = useAuth();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mismatch = confirm !== "" && next !== confirm;
  const tooShort = next !== "" && next.length < MIN_PASSWORD;
  const same = next !== "" && next === current;
  const valid = current !== "" && next.length >= MIN_PASSWORD && next === confirm && !same;

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!valid) return;
    setSubmitting(true);
    setError(null);
    try {
      await api.post("/api/me/change-password", {
        current_password: current,
        new_password: next,
      });
      passwordChanged();
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setError(t("mustChange.wrongCurrent"));
      } else if (err instanceof ApiError) {
        setError(err.problem.detail || err.problem.title);
      } else {
        setError(t("common.error"));
      }
      setSubmitting(false);
    }
  }

  return (
    <main className="mkt-login">
      <form className="mkt-login__form" onSubmit={onSubmit} aria-label={t("mustChange.title")}>
        <h1 className="mkt-login__title">{t("mustChange.title")}</h1>
        <p className="mkt-muted">{t("mustChange.intro")}</p>
        <label className="mkt-field">
          <span className="mkt-field__label">{t("mustChange.current")}</span>
          <input
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            autoComplete="current-password"
            required
            autoFocus
          />
        </label>
        <label className="mkt-field">
          <span className="mkt-field__label">{t("mustChange.new")}</span>
          <input
            type="password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            autoComplete="new-password"
            required
            minLength={MIN_PASSWORD}
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
        {tooShort && (
          <div className="mkt-alert" role="alert">
            {t("mustChange.tooShort")}
          </div>
        )}
        {mismatch && (
          <div className="mkt-alert" role="alert">
            {t("register.mismatch")}
          </div>
        )}
        {same && (
          <div className="mkt-alert" role="alert">
            {t("mustChange.same")}
          </div>
        )}
        {error && (
          <div className="mkt-alert" role="alert">
            {error}
          </div>
        )}
        <button type="submit" className="mkt-btn mkt-btn--primary" disabled={submitting || !valid}>
          {submitting ? t("common.loading") : t("mustChange.submit")}
        </button>
        <button
          type="button"
          className="mkt-btn mkt-btn--ghost"
          // refused: this machine cannot reach its home (lib/auth)
          onClick={() => void logout().catch(() => setError(t("homeOffline.signOut")))}
        >
          {t("mustChange.signOut")}
        </button>
      </form>
    </main>
  );
}
