// What a node shows while it cannot fully serve the app (Epic 35, story 35.10
// AC6). Two signals: any API call answering 503 `paused` / `home_unreachable`
// or a read marked X-Maktaba-Home: unreachable (lib/api dispatches
// NODE_UNAVAILABLE_EVENT), and /api/cluster/self naming a schema-guard reason
// at start. `paused` (this machine's code does not fit the home's database)
// swaps the app for the paused page; `home_unreachable` keeps the app readable
// under a notice (sign-in, sign-out and changes need the home) until the home
// answers again, unless the node said it cannot serve this person even reads
// ({readable: false}: past its offline window, after a restart, a sign-in it
// never saw confirmed), when the paused page takes over too. On the home
// nothing ever changes.
import { useEffect, useState, type ReactNode } from "react";
import {
  NODE_UNAVAILABLE_EVENT,
  blocksApp,
  lastNodeUnavailable,
  noteHomeBack,
  worseUnavailable,
  type NodeUnavailableDetail,
} from "../lib/api";
import { clusterApi } from "../lib/clusterApi";
import { Paused } from "../pages/Paused";
import { HomeOffline } from "./HomeOffline";

export function NodeGuard({ children }: { children: ReactNode }) {
  const [down, setDown] = useState<NodeUnavailableDetail | null>(() => lastNodeUnavailable());

  useEffect(() => {
    let alive = true;
    const onDown = (e: Event) => {
      const d = (e as CustomEvent<NodeUnavailableDetail>).detail;
      if (d && (d.code === "paused" || d.code === "home_unreachable")) {
        setDown((prev) => worseUnavailable(prev, d));
      }
    };
    window.addEventListener(NODE_UNAVAILABLE_EVENT, onDown);
    // an answer that came before this listener
    const early = lastNodeUnavailable();
    if (early) setDown((prev) => worseUnavailable(prev, early));
    clusterApi
      .getSelf()
      .then((s) => {
        if (alive && s.role === "node" && s.paused) {
          setDown((prev) => worseUnavailable(prev, { code: "paused", reason: s.paused }));
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

  if (down && blocksApp(down)) return <Paused code={down.code} reason={down.reason} />;
  return (
    <>
      {down?.code === "home_unreachable" && (
        <HomeOffline
          onBack={() => {
            noteHomeBack();
            // only the home's state clears: a pause that came meanwhile stays
            setDown((p) => (p?.code === "home_unreachable" ? null : p));
          }}
        />
      )}
      {children}
    </>
  );
}
