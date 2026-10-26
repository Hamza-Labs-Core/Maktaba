// The reminder in this Mac's own web app (Maktaba.app running the server): the
// permissions walkthrough once (the shell remembers it was shown), then a
// banner whenever a required permission is off, checked again each time the
// window comes back. Nothing in a browser, in the app connected to another
// Maktaba, or with an older shell that cannot say.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { HelpLink } from "../../help/HelpLink";
import { useI18n } from "../../lib/i18n";
import {
  isLocalServerPage,
  missingRequired,
  tauriPermissionsApi,
  type PermissionsApi,
} from "../../lib/macPermissions";
import { PermissionList, usePermissions } from "./PermissionList";

/** "Later" hides the banner until the app opens again (the page loads again). */
let laterThisRun = false;

/** Test seam. */
export function resetReminder() {
  laterThisRun = false;
}

export function MacPermissionsReminder({
  api = tauriPermissionsApi,
  enabled = isLocalServerPage(),
}: {
  api?: PermissionsApi;
  enabled?: boolean;
}) {
  const { t, locale } = useI18n();
  const { report, refresh } = usePermissions(api, enabled);
  const [open, setOpen] = useState(false);
  const [later, setLater] = useState(laterThisRun);
  const active = !!report && report.server_mode && report.items.length > 0;

  // the walkthrough once: shown, then remembered by the shell
  useEffect(() => {
    if (active && report && !report.seen) setOpen(true);
  }, [active, report]);

  if (!active || !report) return null;
  const missing = missingRequired(report);
  const close = () => {
    setOpen(false);
    if (!report.seen) api.markSeen().then(refresh, () => {});
  };
  const names = missing
    .map((it) => t(`perm.item.${it.id}.title`))
    .join(locale === "ar" ? "، " : ", ");
  return (
    <>
      {missing.length > 0 && !later && !open && (
        <div className="mkt-update-banner mkt-perm-banner" role="status" data-testid="perm-banner">
          <span className="mkt-update-banner__text">{t("perm.banner.text", { items: names })}</span>
          <span className="mkt-update-banner__actions">
            <Button type="button" size="sm" onClick={() => setOpen(true)}>
              {t("perm.banner.fix")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => {
                laterThisRun = true;
                setLater(true);
              }}
            >
              {t("perm.banner.later")}
            </Button>
          </span>
        </div>
      )}
      <Modal
        open={open}
        onClose={close}
        title={t("perm.dialog.title")}
        footer={
          <>
            <HelpLink to={{ topic: "mac-app", anchor: "permissions" }} />
            <Button type="button" onClick={close}>
              {t("perm.dialog.done")}
            </Button>
          </>
        }
      >
        <PermissionList report={report} api={api} onRecheck={refresh} />
      </Modal>
    </>
  );
}
