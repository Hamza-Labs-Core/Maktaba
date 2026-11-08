// The re-authentication dialog (story 50.33 §5): an admin action that hurts (suspend, set a
// plan, block, revoke a license, delete a user's data) needs an MFA check from the last five
// minutes. When one answers 401 reauth_required, this asks for a code from the authenticator
// app (POST /v1/staff/reauth/totp); the session then sends the action again. A password alone
// never counts, and passkeys come in a later part.
import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, useErrorMessage } from "@cloud-shared/ui";
import { acceptSession, api, type StaffSession } from "../api";

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

  useEffect(() => {
    if (open) {
      setCode("");
      setBusy(false);
      setError(null);
    }
  }, [open]);

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
          <Button loading={busy} disabled={!code.trim()} onClick={() => void submit()}>
            {t("staff.reauth.submit")}
          </Button>
        </div>
      }
    >
      <p>{t("staff.reauth.body")}</p>
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <form className="mkc-form" onSubmit={(e) => void submit(e)} noValidate>
        <Input
          label={t("staff.mfa.totp")}
          inputMode="numeric"
          autoComplete="one-time-code"
          value={code}
          onChange={(e) => setCode(e.currentTarget.value)}
        />
      </form>
    </Modal>
  );
}
