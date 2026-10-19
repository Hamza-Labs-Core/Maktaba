// Shows the paused page while this node cannot serve the app (Epic 35,
// story 35.10 AC6). Two signals: any API call answering 503 `paused` /
// `home_unreachable` (lib/api dispatches NODE_UNAVAILABLE_EVENT), and
// /api/cluster/self naming a schema-guard reason at start. Until either
// arrives the app renders as usual; on the home nothing ever changes.
import { useEffect, useState, type ReactNode } from "react";
import {
  NODE_UNAVAILABLE_EVENT,
  lastNodeUnavailable,
  type NodeUnavailableDetail,
} from "../lib/api";
import { clusterApi } from "../lib/clusterApi";
import { Paused } from "../pages/Paused";

export function NodeGuard({ children }: { children: ReactNode }) {
  const [down, setDown] = useState<NodeUnavailableDetail | null>(() => lastNodeUnavailable());

  useEffect(() => {
    let alive = true;
    const onDown = (e: Event) => {
      const d = (e as CustomEvent<NodeUnavailableDetail>).detail;
      if (d && (d.code === "paused" || d.code === "home_unreachable")) {
        // the first reason stays: the page polls for the change itself
        setDown((prev) => prev ?? d);
      }
    };
    window.addEventListener(NODE_UNAVAILABLE_EVENT, onDown);
    // an answer that came before this listener
    const early = lastNodeUnavailable();
    if (early) setDown((prev) => prev ?? early);
    clusterApi
      .getSelf()
      .then((s) => {
        if (alive && s.role === "node" && s.paused) {
          setDown((prev) => prev ?? { code: "paused", reason: s.paused });
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

  if (down) return <Paused code={down.code} reason={down.reason} />;
  return <>{children}</>;
}
