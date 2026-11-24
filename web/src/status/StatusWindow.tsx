// The server's status in its own window of the desktop app (Epic 13 story 13.13; the design:
// docs/design/desktop/mockups/server-status.html). The shell marks the window "status"
// (lib/ownWindow.ts) and main.tsx draws this instead of the app: Maktaba's title bar, a rail
// (the status here; Libraries, Devices and Settings open in the main window; Logs opens the
// log folder), the state with the two actions, a release on its way, one tile per part of the
// server, and the way to the logs and to report a problem. Closing it keeps the server running.
import { useEffect, useState } from "react";
import { DesktopIcon, type DesktopIconName } from "../components/DesktopIcon";
import { FrameStart, WindowControls, useFrameHost } from "../components/DesktopFrame";
import { bidiIsolate, useI18n } from "../lib/i18n";
import { formatBytes, NO_ANSWER } from "../lib/desktopSetup";
import {
  canResume,
  onStatusWindow,
  statusTone,
  statusWindowAction,
  statusWindowState,
  type PartId,
  type StatusAction,
  type StatusPart,
  type StatusUpdate,
  type StatusWindowView,
} from "../lib/statusWindow";
import { startingDetail } from "../pages/Setup/startingDetail";
import { platformWords } from "../pages/Setup/platform";

const PART_ICONS: Record<PartId, DesktopIconName> = {
  database: "db",
  server: "server",
  playback: "live",
  indexing: "search",
  model: "sparkle",
  plugins: "plug",
  remote: "globe",
};

type T = ReturnType<typeof useI18n>["t"];

function act(action: StatusAction) {
  void statusWindowAction(action);
}

export function StatusWindow() {
  const { t } = useI18n();
  // the window's title bar is this page's own (Maktaba's frame: lib/frame.ts)
  const frame = useFrameHost();
  // the words of the machine it runs on ("this PC", "this Mac")
  const words = platformWords(
    t,
    frame?.controls === "mac" ? "macos" : frame?.controls === "gtk" ? "linux" : "windows"
  );
  const [view, setView] = useState<StatusWindowView | null>(null);

  useEffect(() => {
    let live = true;
    let stop = () => {};
    void statusWindowState().then((v) => live && v && setView(v));
    void onStatusWindow((v) => live && setView(v)).then((f) => {
      if (live) stop = f;
      else f();
    });
    return () => {
      live = false;
      stop();
    };
  }, []);

  useEffect(() => {
    document.title = t("statusWindow.title");
  }, [t]);

  return (
    <div className="mkt-dwin mkt-status" data-testid="status-window">
      <header className="mkt-dwin__bar" data-tauri-drag-region="">
        <FrameStart menu={false} />
        <span className="mkt-dwin__app">
          <img src="/icons/favicon-32.png" alt="" width={18} height={18} />
          {t("app.title")}
        </span>
        {view?.host && (
          <bdi className="mkt-dwin__crumb" dir="ltr">
            {view.host}
          </bdi>
        )}
        <span className="mkt-dwin__grow" />
        <WindowControls />
      </header>
      <div className="mkt-status__body">
        <Rail />
        <main className="mkt-status__main" aria-labelledby="mkt-status-h">
          {view ? (
            <Status view={view} words={words} />
          ) : (
            <h1 id="mkt-status-h" className="mkt-visually-hidden">
              {t("statusWindow.title")}
            </h1>
          )}
        </main>
      </div>
    </div>
  );
}

function Rail() {
  const { t } = useI18n();
  const item = (id: string, icon: DesktopIconName, action?: StatusAction) => (
    <button
      type="button"
      className={`mkt-status__nav${action ? "" : " is-on"}`}
      aria-current={action ? undefined : "page"}
      onClick={action ? () => act(action) : undefined}
    >
      <span className="mkt-status__pill">
        <DesktopIcon name={icon} />
      </span>
      <span>{t(`statusWindow.nav.${id}`)}</span>
    </button>
  );
  return (
    <nav className="mkt-status__rail" aria-label={t("statusWindow.nav")}>
      {item("status", "server")}
      {item("libraries", "library", "libraries")}
      {item("devices", "device", "devices")}
      {item("logs", "logs", "logs")}
      <span className="mkt-status__spacer" />
      {item("settings", "settings", "settings")}
    </nav>
  );
}

/** The line under the state: this machine, the release, since when. */
function subLine(
  view: StatusWindowView,
  t: T,
  formatDate: (d: string, o: Intl.DateTimeFormatOptions) => string
) {
  const bits: string[] = [];
  if (view.host) bits.push(t("statusWindow.on", { host: bidiIsolate(view.host) }));
  if (view.version) bits.push(t("statusWindow.version", { version: bidiIsolate(view.version) }));
  if (view.since && view.status.kind === "running") {
    const when = formatDate(view.since, {
      weekday: "long",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    });
    bits.push(t("statusWindow.since", { when }));
  }
  return bits.join(" · ");
}

/** Why the server is not running, in the window's words, when it says. */
function whyLine(view: StatusWindowView, t: T, words: Record<string, string>): string {
  const s = view.status;
  if (s.kind === "not_answering") {
    return s.reason === NO_ANSWER ? t("statusWindow.noAnswer") : s.reason;
  }
  if (s.kind !== "starting" && s.kind !== "problem") return "";
  const said = startingDetail(t, s.detail_code, s.detail_params, words);
  if (said.text) return said.text;
  if (s.detail) return s.detail;
  return s.kind === "problem" ? t("statusWindow.keepsStopping", { roles: s.roles.join(", ") }) : "";
}

function Status({ view, words }: { view: StatusWindowView; words: Record<string, string> }) {
  const { t, formatDate } = useI18n();
  const tone = statusTone(view.status);
  const resume = canResume(view.status);
  const why = whyLine(view, t, words);
  return (
    <>
      <div className="mkt-status__state">
        <span className={`mkt-status__halo is-${tone}`}>
          <DesktopIcon name="server" size={24} />
          <span className={`mkt-ddot is-${tone}`} />
        </span>
        <div className="mkt-status__head">
          <h1 id="mkt-status-h">{t(`statusWindow.state.${view.status.kind}`)}</h1>
          <p className="mkt-status__sub">{subLine(view, t, formatDate)}</p>
        </div>
        <span className="mkt-dwin__grow" />
        {view.status.kind !== "setup_needed" && (
          <button
            type="button"
            className="mkt-dbtn"
            onClick={() => act(resume ? "resume" : "pause")}
          >
            <DesktopIcon name={resume ? "playcircle" : "pausecircle"} size={16} />
            {t(resume ? "statusWindow.resume" : "statusWindow.pause")}
          </button>
        )}
        <button type="button" className="mkt-dbtn mkt-dbtn--primary" onClick={() => act("open")}>
          <DesktopIcon name="external" size={16} dir />
          {t("statusWindow.open")}
        </button>
      </div>
      {why && (
        <p className={`mkt-status__why is-${tone}`} role={tone === "error" ? "alert" : undefined}>
          {why}
        </p>
      )}
      {view.update && <Update update={view.update} />}
      {view.parts.length > 0 ? (
        <ul className="mkt-status__tiles" aria-label={t("statusWindow.parts")}>
          {view.parts.map((p) => (
            <Tile key={p.id} part={p} words={words} />
          ))}
        </ul>
      ) : (
        <p className="mkt-status__empty">{t("statusWindow.noParts")}</p>
      )}
      <div className="mkt-status__foot">
        <button type="button" className="mkt-dbtn mkt-dbtn--ghost" onClick={() => act("logs")}>
          <DesktopIcon name="logs" size={16} />
          {t("statusWindow.logs")}
        </button>
        <button type="button" className="mkt-dbtn mkt-dbtn--ghost" onClick={() => act("report")}>
          <DesktopIcon name="flag" size={16} />
          {t("statusWindow.report")}
        </button>
        <span className="mkt-dwin__grow" />
        <span className="mkt-status__note">{t("statusWindow.closeNote")}</span>
      </div>
    </>
  );
}

function Update({ update }: { update: StatusUpdate }) {
  const { t } = useI18n();
  const version = bidiIsolate(update.version);
  const head = update.installing
    ? t("statusWindow.update.now", { version })
    : update.version
      ? t("statusWindow.update.ready", { version })
      : t("statusWindow.update.readyPlain");
  const rest = update.installing
    ? ""
    : update.install
      ? t("statusWindow.update.whenReady")
      : update.by
        ? t("statusWindow.update.by", { time: bidiIsolate(update.by) })
        : t("statusWindow.update.soon");
  return (
    <div className="mkt-status__update" role="status">
      <DesktopIcon name="update" />
      <p>
        <strong>{head}</strong> {rest && <span className="mkt-status__muted">{rest}</span>}
      </p>
      {update.install && !update.installing && (
        <button type="button" className="mkt-dbtn mkt-dbtn--ghost" onClick={() => act("update")}>
          {t("statusWindow.update.install")}
        </button>
      )}
    </div>
  );
}

/** A tile's value: its state in a word (the model's in its own). */
function value(p: StatusPart, t: T): string {
  if (p.id === "model") {
    if (p.tone === "ok") return t("statusWindow.tone.ready");
    if (p.tone === "busy" && p.bytes) return t("statusWindow.tone.downloading");
  }
  return t(`statusWindow.tone.${p.tone}`);
}

/** A tile's line: what it waits on or why it fails, the model's download, else what it does. */
function line(p: StatusPart, t: T, words: Record<string, string>): string {
  if (p.detail_code || p.detail) {
    const said = startingDetail(t, p.detail_code, p.detail_params, words);
    if (said.text) return said.text;
    if (p.detail) return p.detail;
  }
  if (p.id === "model" && p.model) {
    if (p.bytes) {
      return t("statusWindow.note.modelBytes", {
        name: bidiIsolate(p.model),
        done: bidiIsolate(formatBytes(p.bytes[0])),
        total: bidiIsolate(formatBytes(p.bytes[1])),
      });
    }
    return p.model;
  }
  return t(`statusWindow.note.${p.id}`);
}

function Tile({ part, words }: { part: StatusPart; words: Record<string, string> }) {
  const { t } = useI18n();
  const name = t(`statusWindow.part.${part.id}`);
  const pct = part.progress === undefined ? null : Math.round(part.progress * 100);
  return (
    <li className="mkt-status__tile" data-part={part.id}>
      <div className="mkt-status__tile-head">
        <DesktopIcon name={PART_ICONS[part.id]} size={16} />
        <span>{name}</span>
        <span className={`mkt-ddot is-${part.tone}`} aria-hidden="true" />
      </div>
      <div className="mkt-status__value">{value(part, t)}</div>
      <div className="mkt-status__line">{line(part, t, words)}</div>
      {pct !== null && (
        <div
          className="mkt-dbar"
          role="progressbar"
          aria-label={name}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
        >
          <i style={{ inlineSize: `${pct}%` }} />
        </div>
      )}
    </li>
  );
}
