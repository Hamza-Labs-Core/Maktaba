// A confirm dialog that will not submit without a reason: suspend, unsuspend,
// unblock. The reason goes into the audit log.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { useI18n } from "../../i18n";
import { Banner, useErrorMessage } from "../../ui";

export function ReasonDialog({
  open,
  title,
  body,
  confirmLabel,
  destructive,
  onClose,
  onConfirm,
}: {
  open: boolean;
  title: string;
  body?: string;
  confirmLabel: string;
  destructive?: boolean;
  onClose: () => void;
  /** Resolves when done; a rejection is shown in the dialog. */
  onConfirm: (reason: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const message = useErrorMessage();
  const [reason, setReason] = useState("");
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    if (open) {
      setReason("");
      setTouched(false);
      setError(null);
      setBusy(false);
    }
  }, [open]);

  const missing = reason.trim() === "";

  async function confirm() {
    setTouched(true);
    if (missing) return;
    setBusy(true);
    setError(null);
    try {
      await onConfirm(reason.trim());
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      dismissable={false}
      title={title}
      footer={
        <div className="mkc-row">
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            variant={destructive ? "destructive" : "primary"}
            loading={busy}
            disabled={missing}
            onClick={confirm}
          >
            {confirmLabel}
          </Button>
        </div>
      }
    >
      {body && <p>{body}</p>}
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <Textarea
        label={t("admin.reason")}
        required
        rows={3}
        value={reason}
        error={touched && missing ? t("error.reason_required") : undefined}
        onChange={(e) => setReason(e.target.value)}
        onBlur={() => setTouched(true)}
      />
    </Modal>
  );
}
