// Story 28.5 — the mobile app's update banner: "New version available — v1.4.2", with Get
// update (the release's .apk, else its page), the release notes and Dismiss (hidden until a
// newer version ships). Only in the sideloaded Android app: a store build is updated by its
// store, a browser and the desktop app by other means (lib/appUpdate says why). The check
// itself is lib/appUpdate.
import { useEffect, useState } from "react";
import { useI18n } from "../lib/i18n";
import { nativePlatform, type NativePlatform } from "../lib/native";
import {
  actionUrl,
  appUpdateToOffer,
  bannerOffered,
  dismissVersion,
  distribution,
  type AppRelease,
  type Distribution,
} from "../lib/appUpdate";

export interface UpdateBannerProps {
  /** Test hooks: the app's version, its platform and build, and the check. */
  current?: string;
  platform?: NativePlatform;
  dist?: Distribution;
  offer?: (current: string) => Promise<AppRelease | null>;
}

export function UpdateBanner({
  current = import.meta.env?.VITE_APP_VERSION ?? "dev",
  platform = nativePlatform(),
  dist = distribution(platform),
  offer = appUpdateToOffer,
}: UpdateBannerProps) {
  const { t } = useI18n();
  const [release, setRelease] = useState<AppRelease | null>(null);
  const offered = bannerOffered(platform, dist);

  useEffect(() => {
    if (!offered) return;
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
  }, [current, offered, offer]);

  if (!offered || !release) return null;
  const action = actionUrl(release);
  return (
    <div className="mkt-update-banner" role="status">
      <span className="mkt-update-banner__text">
        {t("update.banner.available", { version: `v${release.version}` })}
      </span>
      <span className="mkt-update-banner__actions">
        <a className="mkt-btn mkt-btn--primary" href={action} target="_blank" rel="noreferrer">
          {t("update.banner.get")}
        </a>
        {/* the release page is the action itself when the release lists no .apk */}
        {action !== release.releaseUrl && (
          <a href={release.releaseUrl} target="_blank" rel="noreferrer">
            {t("update.banner.notes")}
          </a>
        )}
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
