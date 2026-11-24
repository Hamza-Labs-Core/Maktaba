// Maktaba's own window frame in the desktop app (Epic 13 story 13.10; the design:
// docs/design/desktop/README.md, "Components" — title bar, header bar, menus).
//
// <DesktopFrame /> is mounted once, beside the app. On mount it claims the window's frame
// from the shell (lib/frame.ts); when the shell gives it:
//   - the app shell's header becomes the title bar (it calls useFrameHost(): the menu
//     button, the drag region and the window's controls go into it), so there is one bar;
//   - any other page (setup, server starting, sign-in) gets <FrameBar>, a slim title bar;
//   - nothing is drawn in full screen (big screen).
// Outside the desktop app, or for a page the shell does not answer, it draws nothing, and the
// window keeps the system's frame. The frame is a small store (not a context), so the
// component sits beside the app's providers instead of around them.
import {
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent,
} from "react";
import { useI18n } from "../lib/i18n";
import {
  SNAP_DELAY_MS,
  chooseFrameMenu,
  claimFrame,
  frameAction,
  frameMenuItems,
  gtkButtons,
  onFrameState,
  type FrameInfo,
  type FrameMenuEntry,
  type FrameMenuGroup,
  type FrameState,
  type GtkButton,
} from "../lib/frame";

interface FrameSnapshot {
  info: FrameInfo | null;
  state: FrameState;
  /** headers that draw the frame themselves (useFrameHost) */
  hosts: number;
}

const EMPTY: FrameSnapshot = { info: null, state: { maximized: false }, hosts: 0 };
let snapshot: FrameSnapshot = EMPTY;
const listeners = new Set<() => void>();

function update(change: Partial<FrameSnapshot>) {
  snapshot = { ...snapshot, ...change };
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

function useFrame(): FrameSnapshot {
  return useSyncExternalStore(
    subscribe,
    () => snapshot,
    () => EMPTY
  );
}

export function DesktopFrame() {
  const { info, state, hosts } = useFrame();

  useEffect(() => {
    let live = true;
    let stop = () => {};
    void claimFrame().then((i) => {
      if (!live || !i) return;
      update({ info: i, state: { maximized: i.maximized } });
      void onFrameState((s) => update({ state: s })).then((f) => {
        if (live) stop = f;
        else f();
      });
    });
    return () => {
      live = false;
      stop();
      update({ info: null, state: { maximized: false } });
    };
  }, []);

  // the page's styles know the frame: data-frame="windows|gtk|mac", data-frame-side
  useLayoutEffect(() => {
    const root = document.documentElement;
    if (!info || state.fullscreen) {
      delete root.dataset.frame;
      delete root.dataset.frameSide;
      return;
    }
    root.dataset.frame = info.controls;
    root.dataset.frameSide = info.side;
  }, [info, state.fullscreen]);

  return info && hosts === 0 && !state.fullscreen ? <FrameBar /> : null;
}

/**
 * The page's header carries the frame: while it is mounted no FrameBar is drawn. Returns
 * the frame (null when there is none, or in full screen), for the header to draw its parts.
 */
export function useFrameHost(): FrameInfo | null {
  const { info, state } = useFrame();
  useEffect(() => {
    update({ hosts: snapshot.hosts + 1 });
    return () => update({ hosts: snapshot.hosts - 1 });
  }, []);
  return info && !state.fullscreen ? info : null;
}

/** The parts a header puts at its start: the traffic lights' room (Mac), the menu button. */
export function FrameStart() {
  const { info } = useFrame();
  if (!info) return null;
  if (info.controls === "mac") return <span className="mkt-frame__traffic" aria-hidden="true" />;
  const { start } = info.controls === "gtk" ? gtkButtons(info.layout) : { start: [] };
  return (
    <>
      {start.length > 0 && <GtkButtons buttons={start} />}
      <FrameMenuButton />
    </>
  );
}

/** The window's controls, at the end of a header (nothing on the Mac: AppKit draws them). */
export function WindowControls() {
  const { info } = useFrame();
  if (!info || info.controls === "mac") return null;
  if (info.controls === "gtk") {
    const { end } = gtkButtons(info.layout);
    return end.length > 0 ? <GtkButtons buttons={end} /> : null;
  }
  return <CaptionButtons snap={info.snapLayouts} />;
}

/** The slim title bar of a page without the app's header (setup, sign-in…). */
export function FrameBar() {
  const { t } = useI18n();
  return (
    <div className="mkt-frame-bar" data-tauri-drag-region="deep" data-testid="frame-bar">
      <FrameStart />
      <span className="mkt-frame-bar__title">
        <img src="/icons/favicon-32.png" alt="" width={16} height={16} />
        {t("app.title")}
      </span>
      <span className="mkt-frame-bar__grow" />
      <WindowControls />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Windows' caption buttons
// ---------------------------------------------------------------------------

function CaptionButtons({ snap }: { snap: boolean }) {
  const { t } = useI18n();
  const { state } = useFrame();
  const snapTimer = useRef<number | null>(null);
  const stopSnap = () => {
    if (snapTimer.current !== null) window.clearTimeout(snapTimer.current);
    snapTimer.current = null;
  };
  useEffect(() => stopSnap, []);
  const maxLabel = state.maximized ? t("frame.restore") : t("frame.maximize");
  return (
    <div className="mkt-caption" role="group" aria-label={t("frame.window")}>
      <button
        type="button"
        className="mkt-caption__btn"
        aria-label={t("frame.minimize")}
        title={t("frame.minimize")}
        onClick={() => void frameAction("minimize")}
      >
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M0 5.5h10" />
        </svg>
      </button>
      <button
        type="button"
        className="mkt-caption__btn"
        aria-label={maxLabel}
        title={maxLabel}
        data-testid="frame-maximize"
        onMouseEnter={() => {
          if (!snap) return;
          stopSnap();
          snapTimer.current = window.setTimeout(() => {
            snapTimer.current = null;
            void frameAction("snap");
          }, SNAP_DELAY_MS);
        }}
        onMouseLeave={stopSnap}
        onClick={() => {
          stopSnap();
          void frameAction("maximize");
        }}
      >
        {state.maximized ? (
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <path d="M2.5 2.5V1.5a1 1 0 0 1 1-1h5a1 1 0 0 1 1 1v5a1 1 0 0 1-1 1h-1" />
            <rect x=".5" y="2.5" width="7" height="7" rx="1" />
          </svg>
        ) : (
          <svg viewBox="0 0 10 10" aria-hidden="true">
            <rect x=".5" y=".5" width="9" height="9" rx="1.5" />
          </svg>
        )}
      </button>
      <button
        type="button"
        className="mkt-caption__btn mkt-caption__btn--close"
        aria-label={t("frame.close")}
        title={t("frame.close")}
        onClick={() => void frameAction("close")}
      >
        <svg viewBox="0 0 10 10" aria-hidden="true">
          <path d="M.5.5l9 9M9.5.5l-9 9" />
        </svg>
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// GTK's header-bar buttons
// ---------------------------------------------------------------------------

function GtkButtons({ buttons }: { buttons: GtkButton[] }) {
  const { t } = useI18n();
  const { state } = useFrame();
  const label = (b: GtkButton) =>
    b === "minimize"
      ? t("frame.minimize")
      : b === "close"
        ? t("frame.close")
        : state.maximized
          ? t("frame.restore")
          : t("frame.maximize");
  return (
    <div className="mkt-gtk-buttons" role="group" aria-label={t("frame.window")}>
      {buttons.map((b) => (
        <button
          key={b}
          type="button"
          className="mkt-gtk-buttons__btn"
          aria-label={label(b)}
          title={label(b)}
          onClick={() => void frameAction(b)}
        >
          <svg viewBox="0 0 10 10" aria-hidden="true">
            {b === "minimize" && <path d="M2 6.5h6" />}
            {b === "maximize" && <rect x="2" y="2" width="6" height="6" rx="1" />}
            {b === "close" && <path d="M2 2l6 6M8 2l-6 6" />}
          </svg>
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// The title bar's menu: the menu bar's items (Windows and Linux)
// ---------------------------------------------------------------------------

type Row =
  | { kind: "head"; key: string; title: string }
  | { kind: "sep"; key: string }
  | { kind: "item"; key: string; item: FrameMenuEntry };

function rows(groups: FrameMenuGroup[]): Row[] {
  const out: Row[] = [];
  groups.forEach((g, gi) => {
    if (gi > 0) out.push({ kind: "sep", key: `g${gi}` });
    if (g.title) out.push({ kind: "head", key: `h${gi}`, title: g.title });
    g.items.forEach((item, ii) => {
      out.push(
        item.separator
          ? { kind: "sep", key: `s${gi}-${ii}` }
          : { kind: "item", key: `i${gi}-${ii}-${item.id}`, item }
      );
    });
  });
  return out;
}

export function FrameMenuButton() {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [groups, setGroups] = useState<FrameMenuGroup[]>([]);
  const [active, setActive] = useState(-1);
  const menuId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([]);

  const list = useMemo(() => rows(groups), [groups]);
  const enabled = useMemo(
    () => list.map((r, i) => (r.kind === "item" && r.item.enabled ? i : -1)).filter((i) => i >= 0),
    [list]
  );

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (
        !listRef.current?.contains(e.target as Node) &&
        !triggerRef.current?.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  useEffect(() => {
    if (open && active >= 0) itemRefs.current[active]?.focus();
  }, [open, active]);

  async function openMenu() {
    // the items as they are now: a check, an item the server's state turned off
    const g = await frameMenuItems();
    setGroups(g);
    const first = rows(g).findIndex((r) => r.kind === "item" && r.item.enabled);
    setActive(first);
    setOpen(true);
  }

  function close(focusTrigger = true) {
    setOpen(false);
    if (focusTrigger) triggerRef.current?.focus();
  }

  function move(dir: 1 | -1) {
    if (enabled.length === 0) return;
    const pos = enabled.indexOf(active);
    setActive(enabled[(pos + dir + enabled.length) % enabled.length]!);
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      move(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      move(-1);
    } else if (e.key === "Home") {
      e.preventDefault();
      if (enabled.length) setActive(enabled[0]!);
    } else if (e.key === "End") {
      e.preventDefault();
      if (enabled.length) setActive(enabled[enabled.length - 1]!);
    } else if (e.key === "Escape") {
      e.preventDefault();
      close();
    } else if (e.key === "Tab") {
      close(false);
    }
  }

  return (
    <div className="mkt-frame-menu">
      <button
        ref={triggerRef}
        type="button"
        className="mkt-frame-menu__trigger"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        aria-label={t("frame.menu")}
        title={t("frame.menu")}
        data-testid="frame-menu"
        onClick={() => (open ? close() : void openMenu())}
        onKeyDown={(e) => {
          if ((e.key === "ArrowDown" || e.key === "Enter" || e.key === " ") && !open) {
            e.preventDefault();
            void openMenu();
          }
        }}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M4 7h16M4 12h16M4 17h16" />
        </svg>
      </button>
      {open && (
        <div
          ref={listRef}
          id={menuId}
          role="menu"
          aria-label={t("frame.menu")}
          className="mkt-frame-menu__list"
          onKeyDown={onKeyDown}
        >
          {list.map((r, i) => {
            if (r.kind === "sep")
              return <div key={r.key} role="separator" className="mkt-frame-menu__sep" />;
            if (r.kind === "head")
              return (
                <div key={r.key} role="presentation" className="mkt-frame-menu__head">
                  {r.title}
                </div>
              );
            const { item } = r;
            const check = item.checked !== undefined;
            return (
              <button
                key={r.key}
                ref={(el) => {
                  itemRefs.current[i] = el;
                }}
                type="button"
                role={check ? "menuitemcheckbox" : "menuitem"}
                aria-checked={check ? item.checked : undefined}
                aria-disabled={!item.enabled || undefined}
                disabled={!item.enabled}
                tabIndex={i === active ? 0 : -1}
                className="mkt-frame-menu__item"
                onClick={() => {
                  close();
                  void chooseFrameMenu(item.id);
                }}
              >
                <span className="mkt-frame-menu__check" aria-hidden="true">
                  {item.checked ? "✓" : ""}
                </span>
                {item.label}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
