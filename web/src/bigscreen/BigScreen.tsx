// Epic 46 — big-screen mode: a full-screen, 10-foot view of the library for
// the couch (a TV remote, a game controller, the arrow keys or a mouse).
//
// Lazy-loaded at /big/* (App.tsx), outside AppShell. This file is the frame:
// the screens' routes, the tab row, the focus engine (focus/), every input
// source turned into one set of actions (input/), Back, and the "Leave big
// screen?" dialog. See specs/epics/46-big-screen/README.md.
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import { registerMenuRouter } from "../lib/desktop";
import { returnPath, setFullScreen } from "../lib/bigScreen";
import { FocusProvider, useFocusApi } from "./focus/FocusProvider";
import { ACTION_EVENT, isDirection, type ActionDetail, type BigAction } from "./input/actions";
import { keyAction } from "./input/keys";
import { padGlyphs, type PadLayout } from "./input/gamepad";
import { usePads } from "./input/usePads";
import { backTarget, bigPath, depthOf, useBigNav } from "./nav";
import { Dialog } from "./ui";
import { BigHome } from "./screens/Home";
import { BigLibraries } from "./screens/Libraries";
import { BigLibrary } from "./screens/Library";
import { BigAutoCollection, BigCollection, BigPlaylist } from "./screens/Collection";
import { CardMenu } from "./CardMenu";
import { LongPress, cardOf, hasMenu, type CardActions } from "./cards";
import { BigModeContext, useBigMode, type BigMode } from "./mode";
import { BRIDGE_EXIT, listenToLauncher, tellLauncher } from "./input/bridge";
import { BigSeries, BigSeriesList } from "./screens/Series";
import { BigVideo } from "./screens/Video";
import { BigSearch } from "./screens/Search";
import { BigLive, BigLivePlayer } from "./screens/Live";
import { BigSettings } from "./screens/Settings";
import { BigWatch } from "./screens/Watch";
import { useFeatureGate } from "../lib/capabilities";
import { BigFeatureRoute } from "./screens/FeatureOff";
import "./bigscreen.css";

/** How long the pointer may rest before it hides. */
export const POINTER_IDLE_MS = 3000;

const TABS = [
  { to: bigPath.home, key: "big.tab.home" },
  { to: bigPath.libraries, key: "big.tab.libraries" },
  { to: bigPath.seriesList, key: "big.tab.series" },
  { to: bigPath.search, key: "big.tab.search" },
  { to: bigPath.live, key: "big.tab.live" },
  { to: bigPath.settings, key: "big.tab.settings" },
] as const;

/** The screens that own the whole picture (no tab row, no hints). */
function isPlayerPath(pathname: string): boolean {
  return /^\/big\/(watch|live)\/[^/]+/.test(pathname);
}
/** The live player keeps its own TV keys (▲/▼ channels, digits, I, G). */
function isLivePlayerPath(pathname: string): boolean {
  return /^\/big\/live\/[^/]+/.test(pathname);
}

/**
 * Leaves big screen: full screen off, back to the page the user came from. In
 * the TV build there is no page to go back to: the launcher closes the app
 * (outside one, the window is asked to close).
 */
export function useExitBigScreen() {
  const nav = useNavigate();
  const mode = useBigMode();
  return useCallback(() => {
    if (mode === "tv") {
      tellLauncher(BRIDGE_EXIT);
      window.close();
      return;
    }
    void setFullScreen(false);
    nav(returnPath(), { replace: true });
  }, [nav, mode]);
}

export default function BigScreen({ mode = "desktop" }: { mode?: BigMode }) {
  const { dir } = useI18n();
  const rootRef = useRef<HTMLDivElement | null>(null);
  const [idle, setIdle] = useState(false);

  // the pointer hides when it rests
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const wake = () => {
      setIdle(false);
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => setIdle(true), POINTER_IDLE_MS);
    };
    wake();
    window.addEventListener("pointermove", wake);
    return () => {
      window.removeEventListener("pointermove", wake);
      if (timer) clearTimeout(timer);
    };
  }, []);

  return (
    <div
      ref={rootRef}
      className={`bs${idle ? " bs--idle" : ""}`}
      dir={dir}
      data-bs-root
      data-testid="big-screen"
    >
      <BigModeContext.Provider value={mode}>
        <FocusProvider rootRef={rootRef}>
          <Frame />
        </FocusProvider>
      </BigModeContext.Provider>
    </div>
  );
}

function Frame() {
  const location = useLocation();
  const nav = useNavigate();
  const focus = useFocusApi();
  const { go } = useBigNav();
  const exit = useExitBigScreen();
  const [asking, setAsking] = useState(false);
  const [pad, setPad] = useState<PadLayout | null>(null);
  // a card's actions (a long OK or the menu key), and what the last one did
  const [menuFor, setMenuFor] = useState<CardActions | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const player = isPlayerPath(location.pathname);
  const live = isLivePlayerPath(location.pathname);
  const { t } = useI18n();
  const mode = useBigMode();

  const back = useCallback(() => {
    const where = backTarget(location.pathname, depthOf(location.state));
    if (where === "history") nav(-1);
    else if (where === "home") nav(bigPath.home, { replace: true, state: { bs: 0 } });
    else setAsking(true);
  }, [location.pathname, location.state, nav]);

  // one place every input source ends in
  const act = useCallback(
    (action: BigAction, source: ActionDetail["source"]): boolean => {
      const layer = focus.topLayer();
      if (layer) {
        if (action === "back") layer.onBack();
        else if (isDirection(action)) focus.move(action);
        else if (action === "select" && source !== "key") clickFocused();
        else return false;
        return true;
      }
      // the screen first (the player, the search keyboard)
      const ev = new CustomEvent<ActionDetail>(ACTION_EVENT, {
        detail: { action, source },
        cancelable: true,
      });
      window.dispatchEvent(ev);
      if (ev.defaultPrevented) return true;
      // Epic 56 §6.5: Play/Pause plays the focused card; the menu key opens its actions
      const card = player ? null : cardOf(document.activeElement);
      if (card && (action === "playPause" || action === "play")) {
        card.play();
        return true;
      }
      if (hasMenu(card) && action === "menu") {
        setMenuFor(card);
        return true;
      }
      if (action === "back") back();
      else if (isDirection(action)) focus.move(action);
      else if (action === "select") {
        // Enter on a focused button is the browser's own click
        if (source === "key") return false;
        clickFocused();
      } else if (action === "search") go(bigPath.search);
      else if (action === "menu") go(bigPath.settings);
      else return false;
      return true;
    },
    [focus, back, go, player]
  );

  // a long OK on a card opens its actions; a short one presses it
  const longPress = useRef<LongPress | null>(null);
  useEffect(() => {
    if (!notice) return;
    const id = setTimeout(() => setNotice(null), 4000);
    return () => clearTimeout(id);
  }, [notice]);

  // the keyboard (and remotes that present themselves as one)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (live) {
        // the live player keeps its keys (it may have handled this one too);
        // only leaving is ours
        if (["Escape", "BrowserBack", "GoBack"].includes(e.key) && !focus.topLayer()) {
          e.preventDefault();
          back();
        }
        return;
      }
      if (e.defaultPrevented) return;
      const target = e.target as HTMLElement | null;
      const typing =
        !!target?.matches?.(
          "input:not([type=checkbox]):not([type=radio]):not([type=range]), textarea"
        ) || !!target?.isContentEditable;
      const action = keyAction(e, { typing, player });
      if (!action) return;
      if (action === "select" && !player && !focus.topLayer()) {
        const el = document.activeElement as HTMLElement | null;
        const card = cardOf(el);
        if (el && hasMenu(card)) {
          // held: the actions; released early: the card's own press (on key up)
          e.preventDefault();
          if (!longPress.current)
            longPress.current = new LongPress(
              () => el.click(),
              () => setMenuFor(card)
            );
          longPress.current.down(e.repeat);
          return;
        }
      }
      if (act(action, "key")) e.preventDefault();
    };
    const onKeyUp = (e: KeyboardEvent) => {
      const lp = longPress.current;
      if (!lp || keyAction(e, { player }) !== "select") return;
      longPress.current = null;
      e.preventDefault();
      lp.up();
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("keyup", onKeyUp);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("keyup", onKeyUp);
      longPress.current?.cancel();
      longPress.current = null;
    };
  }, [act, back, focus, live, player]);

  // game controllers; on the live player they press its keys
  usePads((action, layout) => {
    setPad(layout);
    if (live && !focus.topLayer() && action !== "back") return pressLiveKey(action);
    act(action, "pad");
  });

  // the TV launcher's bridge (story 58.09): keys it saw first, as actions
  useEffect(() => {
    if (mode !== "tv") return;
    return listenToLauncher((action) => {
      if (live && !focus.topLayer() && action !== "back") return pressLiveKey(action);
      act(action, "bridge");
    });
  }, [mode, act, live, focus]);

  // the desktop tray's Play / Pause
  useEffect(() => {
    let off: (() => void) | null = null;
    let gone = false;
    void registerMenuRouter((a) => {
      if (a.kind === "media-control") act(a.command, "media");
    }).then((d) => {
      if (gone) d();
      else off = d;
    });
    return () => {
      gone = true;
      off?.();
    };
  }, [act]);

  const glyphs = padGlyphs(pad ?? "xbox");
  return (
    <>
      {!player && <Tabs />}
      <main className={`bs-main${player ? " bs-main--player" : ""}`} data-bs-scroll>
        <Routes>
          <Route index element={<BigHome />} />
          <Route path="libraries" element={<BigLibraries />} />
          <Route path="library/:id" element={<BigLibrary />} />
          <Route path="library/:libraryId/collection/:kind/:id" element={<BigAutoCollection />} />
          <Route path="collection/:id" element={<BigCollection />} />
          <Route path="playlist/:id" element={<BigPlaylist />} />
          <Route path="series" element={<BigSeriesList />} />
          <Route path="series/:id" element={<BigSeries />} />
          <Route path="video/:id" element={<BigVideo />} />
          <Route path="watch/:videoId" element={<BigWatch />} />
          <Route path="search" element={<BigSearch />} />
          <Route
            path="live"
            element={
              <BigFeatureRoute id="live_tv">
                <BigLive />
              </BigFeatureRoute>
            }
          />
          <Route
            path="live/:number"
            element={
              <BigFeatureRoute id="live_tv">
                <BigLivePlayer />
              </BigFeatureRoute>
            }
          />
          <Route
            path="settings"
            element={<BigSettings onExit={mode === "tv" ? null : () => setAsking(true)} />}
          />
          <Route path="*" element={<BigHome />} />
        </Routes>
      </main>
      {!player && (
        <footer className="bs-hints" aria-hidden="true">
          <Hint k={pad ? glyphs.select : "Enter"}>{t("big.hint.select")}</Hint>
          <Hint k={pad ? glyphs.back : "Esc"}>{t("big.hint.back")}</Hint>
          <Hint k={pad ? "Y" : "/"}>{t("big.hint.search")}</Hint>
        </footer>
      )}
      {menuFor && <CardMenu card={menuFor} onClose={() => setMenuFor(null)} onDone={setNotice} />}
      {notice && (
        <p className="bs-notice" role="status" data-testid="big-notice">
          {notice}
        </p>
      )}
      {asking && (
        <Dialog
          title={mode === "tv" ? t("tv.exitConfirm") : t("big.exit.title")}
          body={mode === "tv" ? undefined : t("big.exit.body")}
          onClose={() => setAsking(false)}
          testId="big-exit"
        >
          <button
            type="button"
            className="bs-btn"
            data-bs-focus="exit-stay"
            data-bs-autofocus
            onClick={() => setAsking(false)}
          >
            {mode === "tv" ? t("tv.exitConfirm.no") : t("big.exit.stay")}
          </button>
          <button
            type="button"
            className="bs-btn bs-btn--primary"
            data-bs-focus="exit-leave"
            onClick={exit}
          >
            {mode === "tv" ? t("tv.exitConfirm.yes") : t("big.exit.leave")}
          </button>
        </Dialog>
      )}
    </>
  );
}

function Hint({ k, children }: { k: string; children: ReactNode }) {
  return (
    <span className="bs-hint">
      <kbd>{k}</kbd> {children}
    </span>
  );
}

function clickFocused() {
  const el = document.activeElement as HTMLElement | null;
  if (el && el !== document.body) el.click();
}

// A controller on the live player: the keys a TV remote would send it.
const LIVE_KEYS: Partial<Record<BigAction, string>> = {
  up: "ArrowUp",
  down: "ArrowDown",
  next: "ArrowUp",
  previous: "ArrowDown",
  left: "i",
  right: "i",
  select: "Enter",
  search: "g",
  menu: "i",
};

function pressLiveKey(action: BigAction) {
  const key = LIVE_KEYS[action];
  const el = document.querySelector('[data-testid="live-player"]');
  if (!key || !el) return;
  el.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
}

function Tabs() {
  const { t, locale } = useI18n();
  const { top } = useBigNav();
  const { pathname } = useLocation();
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 15_000);
    return () => clearInterval(id);
  }, []);
  // story 51.11: a viewer has no Live tab while Live TV is off; an admin keeps it
  const liveGate = useFeatureGate("live_tv");
  const tabs = TABS.filter((tab) => tab.to !== bigPath.live || liveGate !== "hide");
  const current = (to: string) =>
    to === bigPath.home ? pathname === to || pathname === `${to}/` : pathname.startsWith(to);
  const exit = useExitBigScreen();
  const mode = useBigMode();
  return (
    <header className="bs-top">
      <span className="bs-brand" aria-hidden="true">
        ۞ {t("app.title")}
      </span>
      <nav className="bs-tabs" aria-label={t("big.tabs")} data-bs-group="tabs">
        {tabs.map((tab) => (
          <button
            key={tab.to}
            type="button"
            className="bs-tab"
            data-bs-focus={`tab:${tab.to}`}
            aria-current={current(tab.to) ? "page" : undefined}
            onClick={() => top(tab.to)}
          >
            {t(tab.key)}
          </button>
        ))}
      </nav>
      {/* always in reach, with a mouse or the arrows: the way out is not only Back on Home;
          the TV build has nothing to go back to (Back on Home leaves the app) */}
      {mode !== "tv" && (
        <button
          type="button"
          className="bs-tab bs-leave"
          data-bs-focus="tab:leave"
          data-testid="big-leave"
          onClick={exit}
        >
          {t("big.exit.leave")}
        </button>
      )}
      <time className="bs-clock" dateTime={now.toISOString()}>
        {new Intl.DateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
          hour: "numeric",
          minute: "2-digit",
        }).format(now)}
      </time>
    </header>
  );
}
