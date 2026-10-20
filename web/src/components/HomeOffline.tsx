// The notice a node shows while it cannot reach its home: pages stay readable
// (the node serves reads on the sign-in the home last confirmed), changes are
// refused. It polls /api/cluster/self every HOME_OFFLINE_POLL_MS and calls
// onBack once the node reports the home reachable again.
import { useEffect, useRef } from "react";
import { clusterApi } from "../lib/clusterApi";
import { useI18n } from "../lib/i18n";

export const HOME_OFFLINE_POLL_MS = 5_000;

export function HomeOffline({ onBack }: { onBack: () => void }) {
  const { t } = useI18n();
  const back = useRef(onBack);
  useEffect(() => {
    back.current = onBack;
  });

  useEffect(() => {
    let alive = true;
    const look = () =>
      clusterApi
        .getSelf()
        .then((s) => {
          if (alive && s.home?.reachable === true) back.current();
        })
        .catch(() => {
          // this machine is restarting too: look again later
        });
    const timer = setInterval(() => void look(), HOME_OFFLINE_POLL_MS);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, []);

  return (
    <div className="mkt-home-offline" role="status">
      <strong>{t("homeOffline.title")}</strong> {t("homeOffline.body")}
    </div>
  );
}
