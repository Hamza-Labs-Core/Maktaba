// The notice a node shows while it cannot reach its home: pages stay readable
// and videos it can read play (the node serves them on the sign-in the home
// last confirmed); signing in or out and changes need the home. It polls
// /api/cluster/self every HOME_OFFLINE_POLL_MS and calls onBack once the node
// reports the home reachable again.
import { useEffect, useRef, useState } from "react";
import { NODE_AVAILABLE_EVENT, NODE_UNAVAILABLE_EVENT, homeUnreachable } from "../lib/api";
import { clusterApi } from "../lib/clusterApi";
import { useI18n } from "../lib/i18n";

export const HOME_OFFLINE_POLL_MS = 5_000;

/** Whether this machine cannot reach its home now (follows lib/api's events). */
export function useHomeUnreachable(): boolean {
  const [out, setOut] = useState(homeUnreachable);
  useEffect(() => {
    const sync = () => setOut(homeUnreachable());
    window.addEventListener(NODE_UNAVAILABLE_EVENT, sync);
    window.addEventListener(NODE_AVAILABLE_EVENT, sync);
    sync();
    return () => {
      window.removeEventListener(NODE_UNAVAILABLE_EVENT, sync);
      window.removeEventListener(NODE_AVAILABLE_EVENT, sync);
    };
  }, []);
  return out;
}

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
