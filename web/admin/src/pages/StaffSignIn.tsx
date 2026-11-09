// Staff sign-in (story 50.33 §2): the email and password (POST /v1/staff/login), then the
// two-step check with the short-lived mfa_token it returns: the passkey
// (POST /v1/staff/mfa/passkey/begin, then /finish with the browser's answer), a code from the
// authenticator app (POST /v1/staff/mfa/totp) or one of the recovery codes
// (POST /v1/staff/mfa/recovery). The passkey is offered first when the account has one and the
// browser can use it. An expired or spent mfa_token sends the person back to the first step.
// When the server ended the last session (idle for 15 minutes, or 8 hours old), the screen says
// so.
import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, useErrorMessage } from "@cloud-shared/ui";
import { acceptSession, api, CloudApiError, type StaffSession } from "../api";
import { useStaff } from "../session";
import {
  getPasskey,
  PasskeyCancelled,
  passkeysSupported,
  type RequestOptionsJSON,
} from "../passkey";
import { Turnstile } from "../../../cloud/src/Turnstile";

interface LoginAnswer {
  mfa_token: string;
  methods: string[];
}

type Step = { kind: "password" } | { kind: "mfa"; token: string; methods: string[] };

/** The second factor the step asks for. */
export type Mode = "passkey" | "totp" | "recovery";

/** The factor offered first: the passkey when there is one and the browser has passkeys, then
 * the app's code, then a recovery code. */
export function firstMode(methods: string[], canPasskey: boolean): Mode {
  if (canPasskey && methods.includes("passkey")) return "passkey";
  if (methods.includes("totp")) return "totp";
  if (methods.includes("recovery")) return "recovery";
  return "totp";
}

export function StaffSignIn() {
  const { t } = useI18n();
  const { ended } = useStaff();
  const message = useErrorMessage();
  const [step, setStep] = useState<Step>({ kind: "password" });
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [mode, setMode] = useState<Mode>("totp");
  const canPasskey = passkeysSupported();
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
      setMode(firstMode(r.methods, canPasskey));
      setStep({ kind: "mfa", token: r.mfa_token, methods: r.methods });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
      // a token is good once: the next try needs a fresh one
      if (siteKey) setTurnstileReset((n) => n + 1);
    }
  }

  function failed(err: unknown) {
    // the step's token expired, was used, or took its 5 attempts: start again
    if (err instanceof CloudApiError && err.code === "mfa_token_invalid") {
      setStep({ kind: "password" });
    }
    setError(err instanceof PasskeyCancelled ? t("staff.passkey.failed") : message(err));
    setBusy(false);
  }

  async function verify(e: FormEvent) {
    e.preventDefault();
    if (step.kind !== "mfa") return;
    setBusy(true);
    setError(null);
    try {
      const path = mode === "recovery" ? "/v1/staff/mfa/recovery" : "/v1/staff/mfa/totp";
      const s = await api.post<StaffSession>(
        path,
        { mfa_token: step.token, code: code.trim() },
        { auth: false }
      );
      acceptSession(s);
    } catch (err) {
      failed(err);
    }
  }

  async function usePasskey() {
    if (step.kind !== "mfa") return;
    setBusy(true);
    setError(null);
    try {
      const opts = await api.post<RequestOptionsJSON>(
        "/v1/staff/mfa/passkey/begin",
        { mfa_token: step.token },
        { auth: false }
      );
      const credential = await getPasskey(opts);
      acceptSession(
        await api.post<StaffSession>(
          "/v1/staff/mfa/passkey/finish",
          { mfa_token: step.token, credential },
          { auth: false }
        )
      );
    } catch (err) {
      failed(err);
    }
  }

  // the other factors this account has, as links under the step
  const others = (methods: string[]): Mode[] =>
    (["passkey", "totp", "recovery"] as Mode[]).filter(
      (m) => m !== mode && methods.includes(m) && (m !== "passkey" || canPasskey)
    );
  const otherLabel: Record<Mode, string> = {
    passkey: "staff.mfa.passkey",
    totp: "staff.mfa.useTotp",
    recovery: "staff.mfa.recovery",
  };

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
            {mode === "passkey" ? (
              <Button type="button" loading={busy} onClick={() => void usePasskey()}>
                {t("staff.mfa.passkey")}
              </Button>
            ) : (
              <>
                {mode === "recovery" ? (
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
              </>
            )}
            <div className="mkc-auth__links">
              {others(step.methods).map((m) => (
                <Button
                  key={m}
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setMode(m);
                    setCode("");
                    setError(null);
                  }}
                >
                  {t(otherLabel[m])}
                </Button>
              ))}
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
