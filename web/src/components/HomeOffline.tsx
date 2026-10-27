// The notice a node shows while it cannot reach its home: pages stay readable,
// signing in works and videos it can read play, from the node's own copy of the
// home's database (Epic 37; the notice says how recent it is); changes need the
// home. It polls /api/cluster/self every HOME_OFFLINE_POLL_MS and calls onBack
// once the node reports the home reachable again.
import { useEffect, useRef, useState } from "react";
import { NODE_AVAILABLE_EVENT, NODE_UNAVAILABLE_EVENT, homeUnreachable } from "../lib/api";
import { clusterApi, type NodeCopy } from "../lib/clusterApi";
import { useFmt } from "./queue/format";
import { useI18n } from "../lib/i18n";
import { SERVER_PAGES } from "../lib/edition";

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
  const fmt = useFmt();
  const [copy, setCopy] = useState<NodeCopy | null>(null);
  // a standby home's admin can make it the home if the home is gone for good (this
  // machine answers its admins only: anyone else sees nothing)
  const [standby, setStandby] = useState(false);
  useEffect(() => {
    let alive = true;
    clusterApi
      .emergency()
      .then((s) => {
        if (alive) setStandby(s.standby);
      })
      .catch(() => {
        // not a standby home (or a home): nothing to offer
      });
    return () => {
      alive = false;
    };
  }, []);
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
          if (!alive) return;
          setCopy(s.copy ?? null);
          if (s.home?.reachable === true) back.current();
        })
        .catch(() => {
          // this machine is restarting too: look again later
        });
    void look();
    const timer = setInterval(() => void look(), HOME_OFFLINE_POLL_MS);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, []);

  const applied = copy?.applied_at;
  return (
    <div className="mkt-home-offline" role="status">
      <strong>{t("homeOffline.title")}</strong> {t("homeOffline.body")}
      {applied && (
        <span className="mkt-home-offline__copy">
          {" "}
          {t("homeOffline.copy", { ago: fmt.ago(applied) })}
        </span>
      )}
      {SERVER_PAGES && standby && (
        <span className="mkt-home-offline__standby">
          {" "}
          <a href="/admin/machines">{t("homeOffline.standby")}</a>
        </span>
      )}
    </div>
  );
}
