// The paused page on a node (Epic 35, story 35.10 AC6, design §11.4).
//
// Shown full-screen while this machine cannot serve the app: its code does
// not fit the home's database (`paused`, the schema guard's reason), or it
// cannot reach its home (`home_unreachable`). A whole page, never a blank
// window: a headline and plain words for why (this machine's release and the
// main machine's), what is happening now (an update of the main machine or of
// this one, in flight or not, and when this machine was last asked), and a way
// to keep going (the library on the main machine, Check again). It polls
// /api/cluster/self every POLL_MS: the page reloads when this machine's version
// changes (its update landed) or when the condition it showed clears.
import { useCallback, useEffect, useRef, useState } from "react";
import type { NodeUnavailableCode } from "../lib/api";
import { clusterApi, type ClusterSelf, type SelfUpdate } from "../lib/clusterApi";
import { bidiIsolate, useI18n } from "../lib/i18n";
import { reloadPage, safeHttpUrl } from "../lib/navigation";
import { pausedReason, useClusterFmt } from "../components/cluster/format";

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

/** Why a paused machine is paused: the main machine is behind it, it is behind, or unknown. */
export type PauseKind = "home_behind" | "node_behind" | "unknown";

export function pauseKind(reason: string | undefined): PauseKind {
  const detail = (reason ?? "").replace(/^paused:\s*/, "");
  const why = pausedReason(detail);
  if (why?.key === "machines.reason.pausedHomeBehind") return "home_behind";
  if (why?.key === "machines.reason.pausedNodeBehind") return "node_behind";
  return "unknown";
}

/** v1.2.3 for 1.2.3 or v1.2.3 ("" for none), kept left to right inside any sentence. */
function ver(v: string | undefined): string {
  const s = (v ?? "").trim().replace(/^v/i, "");
  return s ? bidiIsolate(`v${s}`) : "";
}

/** The home's run state, in a few plain words (paused.phase.*). */
function homePhase(u: SelfUpdate): string {
  switch (u.state) {
    case "planned":
    case "home_preparing":
    case "home_prepared":
      return "preparing";
    case "draining":
      return "draining";
    case "home_applying":
      return u.step === "watching"
        ? "checking"
        : u.step === "waiting_app"
          ? "downloading"
          : "installing";
    case "home_committed":
    case "rolling_out":
      return "machines";
    default:
      return "installing";
  }
}

/** This machine's journal state, in a few plain words (paused.phase.*). */
function ownPhase(u: SelfUpdate): string {
  switch (u.state) {
    case "preparing":
    case "ordered":
      return "downloading";
    case "prepared":
    case "app_staged":
    case "awaiting_click":
    case "clicked":
      return "turn";
    case "watching":
      return "checking";
    case "rolling_back":
      return "back";
    default:
      return "installing";
  }
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
  const { t, formatDate } = useI18n();
  const cf = useClusterFmt();
  const [self, setSelf] = useState<ClusterSelf | null>(null);
  const [checkedAt, setCheckedAt] = useState<Date | null>(null);
  const [lookFailed, setLookFailed] = useState(false);
  const [checking, setChecking] = useState(false);
  const version = useRef<string | null>(null);
  const seenHeld = useRef(false);
  const notHeld = useRef(0);
  const reloaded = useRef(false);
  const alive = useRef(true);

  const reload = useCallback(() => {
    if (reloaded.current) return;
    reloaded.current = true;
    onReload();
  }, [onReload]);

  // look asks this machine once; byHand: a person pressed Check again (a condition
  // found gone reloads at once)
  const look = useCallback(
    (byHand = false) =>
      clusterApi
        .getSelf()
        .then((s) => {
          if (!alive.current) return;
          setSelf(s);
          setCheckedAt(new Date());
          setLookFailed(false);
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
              byHand ||
              seenHeld.current ||
              (code === "home_unreachable" && notHeld.current >= 2 && mayReloadUnseen(Date.now()))
            )
              reload();
          }
        })
        .catch(() => {
          // the machine is restarting (an update?): look again later
          if (alive.current) setLookFailed(true);
        }),
    [code, reload]
  );

  useEffect(() => {
    alive.current = true;
    void look();
    const timer = setInterval(() => void look(), PAUSED_POLL_MS);
    return () => {
      alive.current = false;
      clearInterval(timer);
    };
  }, [look]);

  const checkAgain = () => {
    setChecking(true);
    void look(true).finally(() => {
      if (alive.current) setChecking(false);
    });
  };

  const why = (code === "paused" && (self?.paused || reason)) || "";
  const kind: PauseKind = code === "paused" ? pauseKind(why) : "unknown";
  const homeName = self?.home?.name ? bidiIsolate(self.home.name) : null;
  const homeLabel = homeName ?? t("paused.theMain");
  const homeUrl = safeHttpUrl(self?.home?.web_url);
  const here = self?.name ? bidiIsolate(self.name) : null;
  const hereVer = ver(self?.version);
  const homeVer = ver(self?.home?.version);
  const homeUpdate = self?.home?.update;
  const ownUpdate = self?.update;

  const title =
    code !== "paused"
      ? t("paused.unreachableTitle")
      : kind === "home_behind"
        ? t("paused.homeBehind.title")
        : kind === "node_behind"
          ? t("paused.nodeBehind.title")
          : t("paused.title");

  const lead = (() => {
    if (code !== "paused") return t("paused.unreachable");
    if (kind === "home_behind") {
      const library = homeName ? t("paused.libraryNamed", { name: homeName }) : t("paused.library");
      if (hereVer && homeVer)
        return t("paused.homeBehind.lead", { here: hereVer, home: homeVer, library });
      return t("paused.homeBehind.leadNoVersions", { library });
    }
    if (kind === "node_behind") {
      return homeVer
        ? t("paused.nodeBehind.lead", { home: homeVer })
        : t("paused.nodeBehind.leadNoVersion");
    }
    return t("paused.reasonUnknown");
  })();

  const homeNow = (() => {
    if (self?.home && self.home.reachable === false) return t("paused.now.homeAway");
    if (homeUpdate) {
      return t("paused.now.homeUpdating", {
        version: ver(homeUpdate.version),
        phase: t(`paused.phase.${homePhase(homeUpdate)}`),
      });
    }
    if (!self) return t("paused.now.asking");
    return homeVer
      ? t("paused.now.homeIdle", { version: homeVer })
      : t("paused.now.homeIdleNoVersion");
  })();

  const ownNow = (() => {
    if (ownUpdate) {
      return t("paused.now.ownUpdating", {
        version: ver(ownUpdate.version),
        phase: t(`paused.phase.${ownPhase(ownUpdate)}`),
      });
    }
    if (!self) return t("paused.now.asking");
    if (kind === "home_behind") return t("paused.now.ownWaitsForHome", { version: hereVer });
    if (kind === "node_behind")
      return homeUpdate
        ? t("paused.now.ownAfterHome", { version: hereVer })
        : t("paused.now.ownComes", { version: hereVer });
    return hereVer ? t("paused.now.ownOn", { version: hereVer }) : t("paused.now.asking");
  })();

  const checkedNow = lookFailed
    ? t("paused.now.lookFailed")
    : checkedAt
      ? t("paused.now.checkedAt", {
          time: formatDate(checkedAt, { hour: "numeric", minute: "2-digit", second: "2-digit" }),
        })
      : t("paused.now.asking");

  return (
    <main className="mkt-paused" aria-labelledby="mkt-paused-h" data-testid="paused-page">
      <header className="mkt-paused__bar">
        <span className="mkt-paused__brand">{t("app.title")}</span>
        {here && (
          <span className="mkt-paused__machine" data-testid="paused-machine">
            {t("paused.here", { name: here })}
          </span>
        )}
        <span
          className={`mkt-paused__pill mkt-paused__pill--${code === "paused" ? "paused" : "away"}`}
        >
          {code === "paused" ? t("paused.pill") : t("paused.pillAway")}
        </span>
      </header>
      <div className="mkt-paused__body">
        <section className="mkt-paused__hero">
          <h1 id="mkt-paused-h" className="mkt-paused__title">
            {title}
          </h1>
          <p className="mkt-paused__lead" role="status" data-testid="paused-lead">
            {lead}
          </p>
          {code === "paused" && why && (
            <p className="mkt-paused__note" data-testid="paused-detail">
              {cf.reason(why.startsWith("paused") ? why : `paused: ${why}`)}
            </p>
          )}
        </section>

        <section className="mkt-paused__panel" aria-labelledby="mkt-paused-now">
          <h2 id="mkt-paused-now" className="mkt-paused__h2">
            {t("paused.now.title")}
          </h2>
          <dl className="mkt-paused__facts">
            <div className="mkt-paused__fact" data-testid="paused-now-home">
              <dt>{homeName ?? t("paused.now.homeLabel")}</dt>
              <dd>{homeNow}</dd>
            </div>
            <div className="mkt-paused__fact" data-testid="paused-now-own">
              <dt>{t("paused.now.ownLabel")}</dt>
              <dd>{ownNow}</dd>
            </div>
            <div className="mkt-paused__fact" data-testid="paused-now-checked">
              <dt>{t("paused.now.checkedLabel")}</dt>
              <dd>{checkedNow}</dd>
            </div>
          </dl>
        </section>

        <section className="mkt-paused__panel" aria-labelledby="mkt-paused-go">
          <h2 id="mkt-paused-go" className="mkt-paused__h2">
            {t("paused.go.title")}
          </h2>
          {homeUrl ? (
            <p className="mkt-paused__reason">{t("paused.go.body", { home: homeLabel })}</p>
          ) : (
            <p className="mkt-paused__reason" data-testid="paused-home-unknown">
              {t("paused.homeUnknown")}
            </p>
          )}
          <div className="mkt-paused__actions">
            {homeUrl && (
              <a
                className="mkt-btn mkt-btn--primary mkt-paused__cta"
                href={homeUrl}
                data-testid="paused-use-home"
              >
                {t("paused.useLibrary", { home: homeLabel })}
              </a>
            )}
            <button
              type="button"
              className="mkt-btn"
              onClick={checkAgain}
              disabled={checking}
              data-testid="paused-check"
            >
              {checking ? t("paused.checking") : t("paused.checkAgain")}
            </button>
          </div>
          {homeUrl && (
            <p className="mkt-paused__note">
              <a href={homeUrl} dir="ltr" data-testid="paused-home-url">
                {homeUrl}
              </a>
            </p>
          )}
          <p className="mkt-paused__note">{t("paused.autoReload")}</p>
          {homeUrl && (
            <p className="mkt-paused__note">
              <a
                href={`${homeUrl.replace(/\/+$/, "")}/help/machines#${
                  code === "paused" ? "paused-page" : "home-unreachable"
                }`}
                data-testid="paused-help"
              >
                {t("paused.whatItMeans")}
              </a>
            </p>
          )}
        </section>
      </div>
    </main>
  );
}
