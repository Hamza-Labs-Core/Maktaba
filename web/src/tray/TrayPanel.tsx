// The tray's panel (Epic 13 story 13.12; the design: docs/design/desktop/mockups/tray.html):
// the tray menu's entries, drawn as a panel. The status line with its colour at the top, the
// lines under it (the language model, a release the home ordered), the update when it waits
// for a restart, the actions in the tray's own groups, and the two ways to quit as buttons at
// the foot. The arrow keys move between the actions; Escape hides the panel. The words are the
// tray's (the system's language); the page takes that language's direction.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  LINE_IDS,
  QUIT_IDS,
  chooseTrayItem,
  hideTrayPanel,
  onTrayPanelOpened,
  onTrayPanelState,
  trayPanelState,
  type TrayPanelEntry,
  type TrayPanelState,
} from "../lib/trayPanel";

const BRAND = { en: "Maktaba", ar: "مكتبة" } as const;

/**
 * The panel's parts, from the tray's entries in order, whatever their shape (a server that
 * runs, one that cannot start, a failed update, an app connected to another Maktaba): the
 * status line; every other line of text (disabled items: the language model, a release the
 * home ordered, why it cannot start, a failed update) under it; the update when it waits for a
 * restart; every other enabled item in the tray's own groups; and the two ways to quit.
 */
export function partsOf(entries: TrayPanelEntry[]) {
  const status = entries.find((e) => e.id === "status");
  const lines = entries.filter((e) => !e.separator && e.id !== "status" && !e.enabled);
  const update = entries.find((e) => e.id === "update" && e.enabled);
  const quits = entries.filter((e) => QUIT_IDS.has(e.id) && e.enabled);
  const groups: TrayPanelEntry[][] = [[]];
  for (const e of entries) {
    if (e.separator) {
      if (groups[groups.length - 1]!.length) groups.push([]);
    } else if (e.enabled && e !== update && !QUIT_IDS.has(e.id) && !LINE_IDS.has(e.id)) {
      groups[groups.length - 1]!.push(e);
    }
  }
  return { status, lines, update, groups: groups.filter((g) => g.length), quits };
}

export function TrayPanel() {
  const [state, setState] = useState<TrayPanelState | null>(null);
  const buttons = useRef<Array<HTMLButtonElement | null>>([]);

  useEffect(() => {
    let live = true;
    let stop = () => {};
    // (a change heard meanwhile is newer)
    void trayPanelState().then((s) => live && s && setState((now) => now ?? s));
    void onTrayPanelState((s) => setState(s)).then((f) => {
      if (live) stop = f;
      else f();
    });
    return () => {
      live = false;
      stop();
    };
  }, []);

  // the tray's language and its direction
  useLayoutEffect(() => {
    if (!state) return;
    document.documentElement.lang = state.lang;
    document.documentElement.dir = state.lang === "ar" ? "rtl" : "ltr";
    document.title = BRAND[state.lang];
  }, [state]);

  const parts = useMemo(() => (state ? partsOf(state.entries) : null), [state]);

  // the first action takes the focus when the panel opens (its first state, and each time the
  // shell shows it again), never when its words change under a keyboard (a download's percent)
  // (with the panel's first picture, so a key pressed at once is not lost)
  const opened = useRef(false);
  useLayoutEffect(() => {
    if (!parts || opened.current) return;
    opened.current = true;
    buttons.current.find((b) => b && !b.disabled)?.focus();
  }, [parts]);
  useEffect(
    () =>
      onTrayPanelOpened(() => {
        buttons.current.find((b) => b && !b.disabled)?.focus();
      }),
    []
  );

  // the keys work wherever the focus is in the panel, the page's body included (a click on the
  // status line leaves it there)
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        void hideTrayPanel();
        return;
      }
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      e.preventDefault();
      const live = buttons.current.filter((b): b is HTMLButtonElement => !!b && !b.disabled);
      if (!live.length) return;
      const at = live.indexOf(document.activeElement as HTMLButtonElement);
      const step = e.key === "ArrowDown" ? 1 : -1;
      const next =
        at < 0 ? (step > 0 ? 0 : live.length - 1) : (at + step + live.length) % live.length;
      live[next]?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  if (!state || !parts) return <div className="mkt-tray" aria-busy="true" />;

  let n = -1;
  const register = () => {
    n += 1;
    const i = n;
    return (el: HTMLButtonElement | null) => {
      buttons.current[i] = el;
    };
  };

  const choose = (id: string) => () => void chooseTrayItem(id);
  const brand = BRAND[state.lang];

  return (
    <div className="mkt-tray">
      <header className="mkt-tray__top">
        <img src="/icons/icon-192.png" alt="" width={36} height={36} />
        <div className="mkt-tray__status">
          <div className="mkt-tray__line" role="status">
            <span>{parts.status?.label ?? brand}</span>
            {state.tone !== "none" && (
              <span className={`mkt-tray__dot mkt-tray__dot--${state.tone}`} aria-hidden="true" />
            )}
          </div>
          {parts.lines.map((l) => (
            <div key={l.id + l.label} className="mkt-tray__note">
              {l.label}
            </div>
          ))}
        </div>
      </header>
      {parts.update && (
        <button
          ref={register()}
          type="button"
          className="mkt-tray__update"
          onClick={choose(parts.update.id)}
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M20 12a8 8 0 1 1-2.3-5.6" />
            <path d="M20 4.5V9h-4.5" />
          </svg>
          <span>{parts.update.label}</span>
        </button>
      )}
      <nav className="mkt-tray__menu" aria-label={brand}>
        {parts.groups.map((g, gi) => (
          <div key={gi} className="mkt-tray__group">
            {g.map((e) => (
              <button
                key={e.id}
                ref={register()}
                type="button"
                className="mkt-tray__row"
                disabled={!e.enabled}
                onClick={choose(e.id)}
              >
                {e.label}
              </button>
            ))}
          </div>
        ))}
      </nav>
      {parts.quits.length > 0 && (
        <footer className="mkt-tray__foot">
          {parts.quits.map((q) => (
            <button
              key={q.id}
              ref={register()}
              type="button"
              className={
                q.id === "stop-and-quit" ? "mkt-tray__quit mkt-tray__quit--stop" : "mkt-tray__quit"
              }
              onClick={choose(q.id)}
            >
              {q.label}
            </button>
          ))}
        </footer>
      )}
    </div>
  );
}
