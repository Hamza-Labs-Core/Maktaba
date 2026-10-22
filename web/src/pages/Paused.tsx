// The paused page on a node (Epic 35, story 35.10 AC6, design §11.4).
//
// Shown full-screen while this machine cannot serve the app: its code does
// not fit the home's database (`paused`, the schema guard's reason), or it
// cannot reach its home (`home_unreachable`). It names the reason, links to
// the home's address, and polls /api/cluster/self every POLL_MS: the page
// reloads when this machine's version changes (its update landed) or when the
// condition it showed clears.
import { useEffect, useRef, useState } from "react";
import type { NodeUnavailableCode } from "../lib/api";
import { clusterApi, type ClusterSelf } from "../lib/clusterApi";
import { bidiIsolate, useI18n } from "../lib/i18n";
import { reloadPage, safeHttpUrl } from "../lib/navigation";
import { useClusterFmt } from "../components/cluster/format";

export const PAUSED_POLL_MS = 15_000;

/** How rarely a page that never saw the home away reloads on its own (per tab). */
export const PAUSED_RELOAD_EVERY_MS = 2 * 60_000;
const RELOADED_KEY = "mkt-paused-reloaded";

// mayReloadUnseen: a page shown on a moment's answer (the home answered by the first
// look) reloads, but at most every PAUSED_RELOAD_EVERY_MS: an answer that keeps sending
// the app here never makes a reload loop.
function mayReloadUnseen(now: number): boolean {
  try {
    const last = Number(sessionStorage.getItem(RELOADED_KEY) || 0);
    if (now - last < PAUSED_RELOAD_EVERY_MS) return false;
    sessionStorage.setItem(RELOADED_KEY, String(now));
    return true;
  } catch {
    return false;
  }
}

/** Whether self still shows the condition (null: it does not say). */
function holds(code: NodeUnavailableCode, self: ClusterSelf): boolean | null {
  if (self.paused) return true;
  if (code === "home_unreachable") {
    return self.home && typeof self.home.reachable === "boolean" ? !self.home.reachable : null;
  }
  return false;
}

export function Paused({
  code,
  reason,
  onReload = reloadPage,
}: {
  code: NodeUnavailableCode;
  /** the schema guard's text for `paused` */
  reason?: string;
  /** for tests: what "reload" does */
  onReload?: () => void;
}) {
  const { t } = useI18n();
  const cf = useClusterFmt();
  const [self, setSelf] = useState<ClusterSelf | null>(null);
  const version = useRef<string | null>(null);
  const seenHeld = useRef(false);
  const notHeld = useRef(0);
  const reloaded = useRef(false);

  useEffect(() => {
    let alive = true;
    const reload = () => {
      if (reloaded.current) return;
      reloaded.current = true;
      onReload();
    };
    const look = () =>
      clusterApi
        .getSelf()
        .then((s) => {
          if (!alive) return;
          setSelf(s);
          if (version.current === null) version.current = s.version;
          else if (s.version !== version.current) return reload();
          const h = holds(code, s);
          if (h) {
            seenHeld.current = true;
            notHeld.current = 0;
          } else if (h === false) {
            notHeld.current += 1;
            // once this machine has said it holds; or, for the home away, when two
            // looks in a row find it answering (the page came of a moment's blip),
            // rarely: an answer that never reflects it must not reload every poll
            if (
              seenHeld.current ||
              (code === "home_unreachable" && notHeld.current >= 2 && mayReloadUnseen(Date.now()))
            )
              reload();
          }
        })
        .catch(() => {
          // the machine is restarting (an update?): look again later
        });
    void look();
    const timer = setInterval(() => void look(), PAUSED_POLL_MS);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [code, onReload]);

  const why = (code === "paused" && (self?.paused || reason)) || "";
  const homeName = self?.home?.name ? bidiIsolate(self.home.name) : null;
  const homeUrl = safeHttpUrl(self?.home?.web_url);
  const here = self?.name ? bidiIsolate(self.name) : null;

  return (
    <main className="mkt-paused" aria-labelledby="mkt-paused-h">
      <section className="mkt-paused__card">
        <h1 id="mkt-paused-h" className="mkt-paused__title">
          {code === "paused" ? t("paused.title") : t("paused.unreachableTitle")}
        </h1>
        <p className="mkt-paused__reason" role="status">
          {code === "paused"
            ? why
              ? cf.reason(why.startsWith("paused") ? why : `paused: ${why}`)
              : t("paused.reasonUnknown")
            : t("paused.unreachable")}
        </p>
        {here && <p className="mkt-paused__note">{t("paused.here", { name: here })}</p>}
        {homeUrl ? (
          <p className="mkt-paused__reason">
            {t("paused.useHome", { home: homeName ?? t("paused.theHome") })}{" "}
            <a href={homeUrl} dir="ltr">
              {homeUrl}
            </a>
          </p>
        ) : (
          <p className="mkt-paused__note">{t("paused.homeUnknown")}</p>
        )}
        <p className="mkt-paused__note">{t("paused.autoReload")}</p>
        <div className="mkt-paused__actions">
          <button type="button" className="mkt-btn" onClick={onReload}>
            {t("paused.retry")}
          </button>
        </div>
      </section>
    </main>
  );
}
