// The public account flows around sign-in: register, verify email, forgot /
// reset password, and the OAuth landing page.
import { useEffect, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { api } from "../api";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { Banner, Loading, passwordProblem, useErrorMessage } from "../ui";

function AuthCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="mkc-auth">
      <Card header={<h1 className="mkc-auth__title">{title}</h1>}>{children}</Card>
    </div>
  );
}

function BackToSignIn() {
  const { t } = useI18n();
  return (
    <p className="mkc-auth__links">
      <Link to="/sign-in">{t("auth.backToSignIn")}</Link>
    </p>
  );
}

/** The inline policy hint, or the specific problem once the user has typed. */
function usePasswordHint(pw: string): { description: string; error?: string } {
  const { t } = useI18n();
  const problem = pw ? passwordProblem(pw) : null;
  return {
    description: t("password.policy"),
    error: problem ? t(`password.${problem}`) : undefined,
  };
}

export function Register() {
  const { t } = useI18n();
  const message = useErrorMessage();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [sentTo, setSentTo] = useState<string | null>(null);
  const hint = usePasswordHint(password);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (passwordProblem(password)) return;
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, string> = { email: email.trim(), password };
      if (displayName.trim()) body.display_name = displayName.trim();
      await api.post("/v1/auth/register", body, { auth: false });
      setSentTo(email.trim());
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  if (sentTo) {
    return (
      <AuthCard title={t("register.checkTitle")}>
        <p>{t("register.checkBody", { email: sentTo })}</p>
        <BackToSignIn />
      </AuthCard>
    );
  }

  return (
    <AuthCard title={t("register.title")}>
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <form className="mkc-form" onSubmit={submit} noValidate>
        <Input
          label={t("field.email")}
          type="email"
          autoComplete="email"
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <Input
          label={t("field.displayNameOptional")}
          autoComplete="name"
          maxLength={80}
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
        />
        <Input
          label={t("field.password")}
          type="password"
          autoComplete="new-password"
          required
          description={hint.description}
          error={hint.error}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <Button
          type="submit"
          loading={busy}
          disabled={!email.trim() || passwordProblem(password) !== null}
        >
          {t("register.submit")}
        </Button>
      </form>
      <BackToSignIn />
    </AuthCard>
  );
}

export function VerifyEmail() {
  const { t } = useI18n();
  const message = useErrorMessage();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [state, setState] = useState<"working" | "done" | "failed">(token ? "working" : "failed");
  const [error, setError] = useState<unknown>(null);
  // a verification token is single use: StrictMode's second effect run must not post it again
  const started = useRef(false);

  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    api
      .post("/v1/auth/verify-email", { token }, { auth: false })
      .then(() => setState("done"))
      .catch((err: unknown) => {
        setError(err);
        setState("failed");
      });
  }, [token]);

  return (
    <AuthCard title={t("verify.title")}>
      {state === "working" && <Loading />}
      {state === "done" && <Banner tone="success">{t("verify.done")}</Banner>}
      {state === "failed" && (
        <Banner tone="error">{error ? message(error) : t("error.invalid_token")}</Banner>
      )}
      <BackToSignIn />
    </AuthCard>
  );
}

export function ForgotPassword() {
  const { t } = useI18n();
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.post("/v1/auth/password/forgot", { email: email.trim() }, { auth: false });
    } catch {
      // the answer is the same 202 for every address; show the same state
    } finally {
      setBusy(false);
      setSent(true);
    }
  }

  return (
    <AuthCard title={t("forgot.title")}>
      {sent ? (
        <Banner tone="info">{t("forgot.sent")}</Banner>
      ) : (
        <form className="mkc-form" onSubmit={submit} noValidate>
          <p className="mkc-muted">{t("forgot.body")}</p>
          <Input
            label={t("field.email")}
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <Button type="submit" loading={busy} disabled={!email.trim()}>
            {t("forgot.submit")}
          </Button>
        </form>
      )}
      <BackToSignIn />
    </AuthCard>
  );
}

export function ResetPassword() {
  const { t } = useI18n();
  const message = useErrorMessage();
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [done, setDone] = useState(false);
  const hint = usePasswordHint(password);
  const mismatch = confirm !== "" && confirm !== password;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (passwordProblem(password) || mismatch) return;
    setBusy(true);
    setError(null);
    try {
      await api.post("/v1/auth/password/reset", { token, password }, { auth: false });
      setDone(true);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  if (!token) {
    return (
      <AuthCard title={t("reset.title")}>
        <Banner tone="error">{t("error.invalid_token")}</Banner>
        <BackToSignIn />
      </AuthCard>
    );
  }

  return (
    <AuthCard title={t("reset.title")}>
      {done ? (
        <Banner tone="success">{t("reset.done")}</Banner>
      ) : (
        <>
          {error != null && <Banner tone="error">{message(error)}</Banner>}
          <form className="mkc-form" onSubmit={submit} noValidate>
            <Input
              label={t("field.newPassword")}
              type="password"
              autoComplete="new-password"
              required
              description={hint.description}
              error={hint.error}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Input
              label={t("field.confirmPassword")}
              type="password"
              autoComplete="new-password"
              required
              error={mismatch ? t("password.mismatch") : undefined}
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
            <Button
              type="submit"
              loading={busy}
              disabled={passwordProblem(password) !== null || confirm !== password}
            >
              {t("reset.submit")}
            </Button>
          </form>
        </>
      )}
      <BackToSignIn />
    </AuthCard>
  );
}

/** /signed-in — where the OAuth callback lands with the refresh cookie set. */
export function SignedIn() {
  const { t } = useI18n();
  const { restore } = useSession();
  const navigate = useNavigate();
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void restore().then((ok) =>
      navigate(ok ? "/account" : "/sign-in?error=no_session", { replace: true })
    );
  }, [restore, navigate]);

  return (
    <AuthCard title={t("signedIn.title")}>
      <Loading />
    </AuthCard>
  );
}
