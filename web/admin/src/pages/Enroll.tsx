// /enroll#<token> — a new staff account's setup (story 50.33 §1). The CLI's one-time link
// carries its token in the URL fragment, which the browser never sends: it goes only in the
// POST bodies here. POST /v1/staff/enroll/start draws a TOTP secret for the authenticator app
// (shown as a key to type in and an otpauth link; the web workspace has no QR library); the
// person then sets a password (14–256 characters) and types a code from the app
// (POST /v1/staff/enroll/finish), and gets 10 recovery codes, shown once. Passkeys come in a
// later part.
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useLocation } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, Loading, useErrorMessage } from "@cloud-shared/ui";
import { api, CloudApiError } from "../api";

interface Started {
  email: string;
  totp_secret: string;
  totp_uri: string;
}

/** The staff password rule the API applies (§1), checked here first to save a round trip. */
export const PASSWORD_MIN = 14;
export const PASSWORD_MAX = 256;

/** A base32 secret in groups of four, easier to type into an app. */
export const groupSecret = (s: string): string => s.replace(/(.{4})(?=.)/g, "$1 ");

type State =
  | { kind: "starting" }
  | { kind: "expired" }
  | { kind: "error"; error: unknown }
  | { kind: "ready"; started: Started }
  | { kind: "done"; codes: string[] };

export function Enroll() {
  const { t } = useI18n();
  const message = useErrorMessage();
  const { hash } = useLocation();
  const token = decodeURIComponent(hash.replace(/^#/, ""));
  const [state, setState] = useState<State>(token ? { kind: "starting" } : { kind: "expired" });
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  // each start draws a new secret, so it runs once (React's dev double effect included)
  const started = useRef(false);

  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    api
      .post<Started>("/v1/staff/enroll/start", { token }, { auth: false })
      .then((s) => setState({ kind: "ready", started: s }))
      .catch((err: unknown) =>
        setState(
          err instanceof CloudApiError && err.status === 410
            ? { kind: "expired" }
            : { kind: "error", error: err }
        )
      );
  }, [token]);

  async function finish(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (password.length < PASSWORD_MIN || password.length > PASSWORD_MAX) {
      setError(
        t(password.length < PASSWORD_MIN ? "error.password_too_short" : "error.password_too_long")
      );
      return;
    }
    setBusy(true);
    try {
      const r = await api.post<{ recovery_codes: string[] }>(
        "/v1/staff/enroll/finish",
        { token, password, code: code.trim() },
        { auth: false }
      );
      setPassword("");
      setState({ kind: "done", codes: r.recovery_codes });
    } catch (err) {
      if (err instanceof CloudApiError && err.status === 410) setState({ kind: "expired" });
      else setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function copy(secret: string) {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
    } catch {
      // no clipboard (an insecure page, a denied permission): the key is on screen to select
    }
  }

  let body;
  switch (state.kind) {
    case "starting":
      body = <Loading />;
      break;
    case "expired":
      body = <Banner tone="error">{t("staff.enroll.linkExpired")}</Banner>;
      break;
    case "error":
      body = <Banner tone="error">{message(state.error)}</Banner>;
      break;
    case "done":
      body = (
        <>
          <Banner tone="success">{t("staff.enroll.done")}</Banner>
          <p>{t("staff.enroll.recoveryCodes")}</p>
          <ol className="mkc-codes mkc-mono" aria-label={t("staff.mfa.recoveryCode")}>
            {state.codes.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ol>
          <p>
            <Link to="/">{t("staff.enroll.signIn")}</Link>
          </p>
        </>
      );
      break;
    case "ready": {
      const s = state.started;
      body = (
        <>
          <p>{t("staff.enroll.intro", { email: s.email })}</p>
          <h2>{t("staff.enroll.totp")}</h2>
          <p>
            <a href={s.totp_uri}>{t("staff.enroll.openApp")}</a>
          </p>
          <p>{t("staff.enroll.secret")}</p>
          <div className="mkc-row">
            <code className="mkc-mono" dir="ltr" data-testid="totp-secret">
              {groupSecret(s.totp_secret)}
            </code>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => void copy(s.totp_secret)}
            >
              {copied ? t("staff.enroll.copied") : t("staff.enroll.copy")}
            </Button>
          </div>
          {error && <Banner tone="error">{error}</Banner>}
          <form className="mkc-form" onSubmit={(e) => void finish(e)} noValidate>
            <Input
              label={t("staff.enroll.password")}
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.currentTarget.value)}
            />
            <Input
              label={t("staff.enroll.code")}
              inputMode="numeric"
              autoComplete="one-time-code"
              value={code}
              onChange={(e) => setCode(e.currentTarget.value)}
            />
            <Button type="submit" loading={busy} disabled={!password || !code.trim()}>
              {t("staff.enroll.submit")}
            </Button>
          </form>
        </>
      );
      break;
    }
  }

  return (
    <div className="mkc-auth">
      <Card header={<h1 className="mkc-auth__title">{t("staff.enroll.title")}</h1>}>{body}</Card>
    </div>
  );
}
