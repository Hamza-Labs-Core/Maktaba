// /sign-in — email + password, plus Google / Apple when the cloud has them on.
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { api, apiUrl, CloudApiError } from "../api";
import { useI18n } from "../i18n";
import { navigateExternal } from "../nav";
import { useSession } from "../session";
import { Banner, useErrorMessage } from "../ui";

interface Providers {
  google: boolean;
  apple: boolean;
}

export function SignIn() {
  const { t, has } = useI18n();
  const message = useErrorMessage();
  const { signIn } = useSession();
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<CloudApiError | Error | null>(null);
  const [resent, setResent] = useState(false);
  const [providers, setProviders] = useState<Providers>({ google: false, apple: false });

  useEffect(() => {
    api
      .get<Providers>("/v1/auth/providers", { auth: false })
      .then((p) => setProviders({ google: !!p?.google, apple: !!p?.apple }))
      .catch(() => setProviders({ google: false, apple: false }));
  }, []);

  const oauthError = params.get("error");
  const notice = params.get("notice");
  const from = (location.state as { from?: string } | null)?.from;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setResent(false);
    try {
      await signIn(email.trim(), password);
      navigate(from && from.startsWith("/") ? from : "/account", { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setBusy(false);
    }
  }

  async function resend() {
    try {
      await api.post("/v1/auth/verify-email/resend", { email: email.trim() }, { auth: false });
    } catch {
      // 202 always; a failure (rate limit) still gets the same answer
    }
    setResent(true);
  }

  const unverified = error instanceof CloudApiError && error.code === "email_not_verified";

  return (
    <div className="mkc-auth">
      <Card header={<h1 className="mkc-auth__title">{t("signIn.title")}</h1>}>
        {oauthError && (
          <Banner tone="error">
            {has(`oauthError.${oauthError}`)
              ? t(`oauthError.${oauthError}`)
              : t("oauthError.generic", { code: oauthError })}
          </Banner>
        )}
        {notice && has(`notice.${notice}`) && <Banner tone="info">{t(`notice.${notice}`)}</Banner>}
        {error && (
          <Banner tone="error">
            {message(error)}
            {unverified && (
              <div className="mkc-row mkc-row--tight">
                {resent ? (
                  <span>{t("signIn.resent")}</span>
                ) : (
                  <Button size="sm" variant="secondary" onClick={resend}>
                    {t("signIn.resend")}
                  </Button>
                )}
              </div>
            )}
          </Banner>
        )}
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
            label={t("field.password")}
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <Button type="submit" loading={busy} disabled={!email.trim() || !password}>
            {t("signIn.submit")}
          </Button>
        </form>
        {(providers.google || providers.apple) && (
          <div className="mkc-form mkc-auth__providers">
            {providers.google && (
              <Button
                variant="secondary"
                onClick={() => navigateExternal(apiUrl("/v1/auth/oauth/google"))}
              >
                {t("signIn.google")}
              </Button>
            )}
            {providers.apple && (
              <Button
                variant="secondary"
                onClick={() => navigateExternal(apiUrl("/v1/auth/oauth/apple"))}
              >
                {t("signIn.apple")}
              </Button>
            )}
          </div>
        )}
        <p className="mkc-auth__links">
          <Link to="/forgot-password">{t("signIn.forgot")}</Link>
          <Link to="/register">{t("signIn.register")}</Link>
        </p>
      </Card>
    </div>
  );
}
