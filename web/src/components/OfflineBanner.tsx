// The notice the app shows while the browser has no network: pages already
// loaded stay readable, and a page that failed to load tries again once the
// network is back (LoadError).
import { useOnline } from "../lib/useOnline";
import { useI18n } from "../lib/i18n";

export function OfflineBanner() {
  const online = useOnline();
  const { t } = useI18n();
  if (online) return null;
  return (
    <div className="mkt-home-offline" role="status" data-testid="offline-banner">
      {t("error.offlineBanner")}
    </div>
  );
}
