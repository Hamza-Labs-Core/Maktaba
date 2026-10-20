// What a node shows while it cannot fully serve the app (Epic 35, story 35.10
// AC6). Two signals: any API call answering 503 `paused` / `home_unreachable`
// or a read marked X-Maktaba-Home: unreachable (lib/api dispatches
// NODE_UNAVAILABLE_EVENT), and /api/cluster/self naming a schema-guard reason
// at start. `paused` (this machine's code does not fit the home's database)
// swaps the app for the paused page; `home_unreachable` keeps the app readable
// under a notice (changes are refused) until the home answers again. On the
// home nothing ever changes.
import { useEffect, useState, type ReactNode } from "react";
import {
  NODE_UNAVAILABLE_EVENT,
  clearNodeUnavailable,
  lastNodeUnavailable,
  type NodeUnavailableDetail,
} from "../lib/api";
import { clusterApi } from "../lib/clusterApi";
import { Paused } from "../pages/Paused";
import { HomeOffline } from "./HomeOffline";

// `paused` outranks `home_unreachable`: it is not something waiting fixes
function worse(prev: NodeUnavailableDetail | null, next: NodeUnavailableDetail) {
  return prev?.code === "paused" ? prev : next;
}

export function NodeGuard({ children }: { children: ReactNode }) {
  const [down, setDown] = useState<NodeUnavailableDetail | null>(() => lastNodeUnavailable());

  useEffect(() => {
    let alive = true;
    const onDown = (e: Event) => {
      const d = (e as CustomEvent<NodeUnavailableDetail>).detail;
      if (d && (d.code === "paused" || d.code === "home_unreachable")) {
        setDown((prev) => worse(prev, d));
      }
    };
    window.addEventListener(NODE_UNAVAILABLE_EVENT, onDown);
    // an answer that came before this listener
    const early = lastNodeUnavailable();
    if (early) setDown((prev) => worse(prev, early));
    clusterApi
      .getSelf()
      .then((s) => {
        if (alive && s.role === "node" && s.paused) {
          setDown((prev) => worse(prev, { code: "paused", reason: s.paused }));
        }
      })
      .catch(() => {
        // an older server, or the home: nothing to guard
      });
    return () => {
      alive = false;
      window.removeEventListener(NODE_UNAVAILABLE_EVENT, onDown);
    };
  }, []);

  if (down?.code === "paused") return <Paused code={down.code} reason={down.reason} />;
  return (
    <>
      {down?.code === "home_unreachable" && (
        <HomeOffline
          onBack={() => {
            clearNodeUnavailable();
            setDown(null);
          }}
        />
      )}
      {children}
    </>
  );
}
