// The re-authentication dialog (story 50.33 §5): an admin action that hurts (suspend, set a
// plan, block, revoke a license, delete a user's data) needs an MFA check from the last five
// minutes. When one answers 401 reauth_required, this asks for the account's passkey
// (POST /v1/staff/reauth/passkey/begin, then /finish) or a code from the authenticator app
// (POST /v1/staff/reauth/totp), whichever it has (GET /v1/staff/me); the session then sends
// the action again. A password alone never counts.
import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, useErrorMessage } from "@cloud-shared/ui";
import { acceptSession, api, type StaffSession } from "../api";
import {
  getPasskey,
  PasskeyCancelled,
  passkeysSupported,
  type RequestOptionsJSON,
} from "../passkey";

export function ReauthDialog({
  open,
  onDone,
}: {
  open: boolean;
  /** true once the code was accepted (the new token is already kept); false on cancel. */
  onDone: (ok: boolean) => void;
}) {
  const { t } = useI18n();
  const message = useErrorMessage();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  // the account's factors: the passkey button and the code field each show when it has one
  const [methods, setMethods] = useState<string[] | null>(null);
  const canPasskey = passkeysSupported();

  useEffect(() => {
    if (!open) return;
    setCode("");
    setBusy(false);
    setError(null);
    let live = true;
    api
      .get<{ methods: string[] }>("/v1/staff/me")
      .then((m) => live && setMethods(m.methods))
      // unknown: offer both, and the server says which one it takes
      .catch(() => live && setMethods(["passkey", "totp"]));
    return () => {
      live = false;
    };
  }, [open]);

  const withPasskey = canPasskey && (methods?.includes("passkey") ?? false);
  const withCode = methods == null || methods.includes("totp") || !withPasskey;

  async function passkey() {
    setBusy(true);
    setError(null);
    try {
      const opts = await api.post<RequestOptionsJSON>("/v1/staff/reauth/passkey/begin");
      const credential = await getPasskey(opts);
      acceptSession(
        await api.post<StaffSession>("/v1/staff/reauth/passkey/finish", { credential })
      );
      onDone(true);
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  async function submit(e?: FormEvent) {
    e?.preventDefault();
    if (!code.trim()) return;
    setBusy(true);
    setError(null);
    try {
      acceptSession(await api.post<StaffSession>("/v1/staff/reauth/totp", { code: code.trim() }));
      onDone(true);
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={() => onDone(false)}
      dismissable={false}
      title={t("staff.reauth.title")}
      footer={
        <div className="mkc-row">
          <Button variant="secondary" onClick={() => onDone(false)}>
            {t("common.cancel")}
          </Button>
          {withCode && (
            <Button loading={busy} disabled={!code.trim()} onClick={() => void submit()}>
              {t("staff.reauth.submit")}
            </Button>
          )}
        </div>
      }
    >
      <p>{t("staff.reauth.body")}</p>
      {error != null && (
        <Banner tone="error">
          {error instanceof PasskeyCancelled ? t("staff.passkey.failed") : message(error)}
        </Banner>
      )}
      {withPasskey && (
        <Button type="button" loading={busy} onClick={() => void passkey()}>
          {t("staff.mfa.passkey")}
        </Button>
      )}
      {withCode && (
        <form className="mkc-form" onSubmit={(e) => void submit(e)} noValidate>
          <Input
            label={t("staff.mfa.totp")}
            inputMode="numeric"
            autoComplete="one-time-code"
            value={code}
            onChange={(e) => setCode(e.currentTarget.value)}
          />
        </form>
      )}
    </Modal>
  );
}
