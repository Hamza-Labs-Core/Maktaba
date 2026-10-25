// Story 28.5 — the mobile app's update banner: "New version available — v1.4.2", with the
// store (or .apk) action for this phone, the release notes and Dismiss (hidden until a newer
// version ships). Only inside the Capacitor app: a browser and the desktop app update by
// other means (the server's page, the desktop updater). The check itself is lib/appUpdate.
import { useEffect, useState } from "react";
import { useI18n } from "../lib/i18n";
import { nativePlatform, type NativePlatform } from "../lib/native";
import { actionUrl, appUpdateToOffer, dismissVersion, type AppRelease } from "../lib/appUpdate";

export interface UpdateBannerProps {
  /** Test hooks: the app's version, its platform and the check. */
  current?: string;
  platform?: NativePlatform;
  offer?: (current: string) => Promise<AppRelease | null>;
}

export function UpdateBanner({
  current = import.meta.env?.VITE_APP_VERSION ?? "dev",
  platform = nativePlatform(),
  offer = appUpdateToOffer,
}: UpdateBannerProps) {
  const { t } = useI18n();
  const [release, setRelease] = useState<AppRelease | null>(null);

  useEffect(() => {
    if (platform === "web") return;
    let alive = true;
    offer(current)
      .then((r) => {
        if (alive) setRelease(r);
      })
      .catch(() => {
        // a check that failed shows nothing; the next launch tries again
      });
    return () => {
      alive = false;
    };
  }, [current, platform, offer]);

  if (platform === "web" || !release) return null;
  return (
    <div className="mkt-update-banner" role="status">
      <span className="mkt-update-banner__text">
        {t("update.banner.available", { version: `v${release.version}` })}
      </span>
      <span className="mkt-update-banner__actions">
        <a
          className="mkt-btn mkt-btn--primary"
          href={actionUrl(release, platform)}
          target="_blank"
          rel="noreferrer"
        >
          {t("update.banner.get")}
        </a>
        <a href={release.releaseUrl} target="_blank" rel="noreferrer">
          {t("update.banner.notes")}
        </a>
        <button
          type="button"
          className="mkt-btn mkt-btn--ghost"
          onClick={() => {
            dismissVersion(release.version);
            setRelease(null);
          }}
        >
          {t("common.dismiss")}
        </button>
      </span>
    </div>
  );
}
