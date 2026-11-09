// Staff sign-in (story 50.33 §2): the email and password (POST /v1/staff/login), then the
// two-step check with the short-lived mfa_token it returns: a code from the authenticator app
// (POST /v1/staff/mfa/totp) or one of the recovery codes (POST /v1/staff/mfa/recovery). An
// expired or spent mfa_token sends the person back to the first step. When the server ended the
// last session (idle for 15 minutes, or 8 hours old), the screen says so. Passkeys come in a
// later part.
import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, useErrorMessage } from "@cloud-shared/ui";
import { acceptSession, api, CloudApiError, type StaffSession } from "../api";
import { useStaff } from "../session";
import { Turnstile } from "../../../cloud/src/Turnstile";

interface LoginAnswer {
  mfa_token: string;
  methods: string[];
}

type Step = { kind: "password" } | { kind: "mfa"; token: string; methods: string[] };

export function StaffSignIn() {
  const { t } = useI18n();
  const { ended } = useStaff();
  const message = useErrorMessage();
  const [step, setStep] = useState<Step>({ kind: "password" });
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // the bot check (story 50.30): its site key from GET /v1/staff/config; "" when off
  const [siteKey, setSiteKey] = useState("");
  const [turnstileToken, setTurnstileToken] = useState("");
  const [turnstileReset, setTurnstileReset] = useState(0);

  useEffect(() => {
    let live = true;
    api
      .get<{ turnstile_site_key?: string }>("/v1/staff/config", { auth: false })
      .then((r) => live && setSiteKey(r?.turnstile_site_key ?? ""))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);

  async function login(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const r = await api.post<LoginAnswer>(
        "/v1/staff/login",
        { email: email.trim(), password, turnstile_token: turnstileToken || undefined },
        { auth: false }
      );
      setPassword("");
      setCode("");
      // a code from the app when the account has one, else a recovery code
      setRecovery(!r.methods.includes("totp") && r.methods.includes("recovery"));
      setStep({ kind: "mfa", token: r.mfa_token, methods: r.methods });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
      // a token is good once: the next try needs a fresh one
      if (siteKey) setTurnstileReset((n) => n + 1);
    }
  }

  async function verify(e: FormEvent) {
    e.preventDefault();
    if (step.kind !== "mfa") return;
    setBusy(true);
    setError(null);
    try {
      const path = recovery ? "/v1/staff/mfa/recovery" : "/v1/staff/mfa/totp";
      const s = await api.post<StaffSession>(
        path,
        { mfa_token: step.token, code: code.trim() },
        { auth: false }
      );
      acceptSession(s);
    } catch (err) {
      // the step's token expired, was used, or took its 5 attempts: start again
      if (err instanceof CloudApiError && err.code === "mfa_token_invalid") {
        setStep({ kind: "password" });
      }
      setError(message(err));
      setBusy(false);
    }
  }

  function restart() {
    setStep({ kind: "password" });
    setError(null);
    setCode("");
  }

  const notice =
    ended === "idle"
      ? t("staff.session.idle")
      : ended === "capped"
        ? t("staff.session.capped")
        : null;

  return (
    <div className="mkc-auth">
      <Card
        header={
          <h1 className="mkc-auth__title">
            {step.kind === "password" ? t("staff.signIn.title") : t("staff.mfa.title")}
          </h1>
        }
      >
        {notice && !error && <Banner tone="info">{notice}</Banner>}
        {error && <Banner tone="error">{error}</Banner>}
        {step.kind === "password" ? (
          <form className="mkc-form" onSubmit={(e) => void login(e)} noValidate>
            <Input
              label={t("field.email")}
              type="email"
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.currentTarget.value)}
            />
            <Input
              label={t("field.password")}
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.currentTarget.value)}
            />
            {siteKey && (
              <Turnstile siteKey={siteKey} onToken={setTurnstileToken} resetKey={turnstileReset} />
            )}
            <Button
              type="submit"
              loading={busy}
              disabled={!email.trim() || !password || (!!siteKey && !turnstileToken)}
            >
              {t("staff.signIn.submit")}
            </Button>
          </form>
        ) : (
          <form className="mkc-form" onSubmit={(e) => void verify(e)} noValidate>
            {recovery ? (
              <>
                <p>{t("staff.mfa.recoveryHint")}</p>
                <Input
                  label={t("staff.mfa.recoveryCode")}
                  autoComplete="off"
                  spellCheck={false}
                  value={code}
                  onChange={(e) => setCode(e.currentTarget.value)}
                />
              </>
            ) : (
              <Input
                label={t("staff.mfa.totp")}
                inputMode="numeric"
                autoComplete="one-time-code"
                value={code}
                onChange={(e) => setCode(e.currentTarget.value)}
              />
            )}
            <Button type="submit" loading={busy} disabled={!code.trim()}>
              {t("staff.signIn.submit")}
            </Button>
            <div className="mkc-auth__links">
              {recovery
                ? step.methods.includes("totp") && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => setRecovery(false)}
                    >
                      {t("staff.mfa.useTotp")}
                    </Button>
                  )
                : step.methods.includes("recovery") && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => setRecovery(true)}
                    >
                      {t("staff.mfa.recovery")}
                    </Button>
                  )}
              <Button type="button" variant="ghost" size="sm" onClick={restart}>
                {t("staff.signIn.back")}
              </Button>
            </div>
          </form>
        )}
      </Card>
    </div>
  );
}
