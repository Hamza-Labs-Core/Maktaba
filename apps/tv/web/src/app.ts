// Story 58.09 — the launcher: finding the home, checking it fits, and hosting its /tv/.
//
//   start      the saved homes (the last used opens at once), Find on this network, and
//              under More ways, Enter the home's address
//   find       the subnet probe of the TV's /24 (probe.ts); each home found is listed
//   typed      an address typed with the remote
//   connect    GET /api/discovery/home, the HTTPS name or the plain-HTTP fallback
//              (discovery.ts), the compatibility check (compat.ts): Update the home or
//              Update this app, and the iframe is never loaded
//   frame      the home's /tv/?bridge=1 full screen, the bridge (bridge.ts) to it, the video
//              plane under it
//
// Sign-in is /tv/'s, in the home's origin: over plain HTTP, or when the UI is bundled, it
// signs in by code only (the home's own code), and the launcher never sees a credential.

import { LauncherBridge, type BridgeAction, type HelloInfo } from "./bridge";
import { decide, type Verdict } from "./compat";
import {
  chooseConnection,
  normalizeBase,
  originOf,
  readHome,
  type Connection,
  type FetchLike,
  type HomeAnswer,
} from "./discovery";
import { forgetHome, rememberHome, savedHomes, type SavedHome } from "./homes";
import { dir, lang, onLangChange, setLang, t, type Lang } from "./i18n";
import { log } from "./log";
import { VideoElementPlayer, type PlayerAdapter } from "./player";
import { probeSubnet, type FoundHome } from "./probe";
import { OLDEST_CHROMIUM, chromiumMajor, type Platform } from "./platform/types";
import { button, h, ltr } from "./ui/dom";
import { focusFirst, moveFocus, type Move } from "./ui/focus";
import {
  APP_NAME,
  BUILT_FOR_API_LEVEL,
  BUNDLE_UI,
  LAUNCHER_VERSION,
  MIN_API_LEVEL,
} from "./version";

/** How long a home gets to answer discovery when connecting. */
export const CONNECT_TIMEOUT_MS = 5000;
/** How long /tv/ gets to say it listens before the launcher calls it unreachable. */
export const READY_TIMEOUT_MS = 20000;

export interface LauncherDeps {
  window: Window;
  platform: Platform;
  fetch: FetchLike;
  /** The player adapter (58.10 and 58.11 pass AVPlay's and webOS's); the <video> one if unset. */
  player?: (video: HTMLVideoElement) => PlayerAdapter;
}

type Screen =
  | { kind: "start"; picker: boolean }
  | { kind: "find" }
  | { kind: "typed"; error?: string }
  | { kind: "connecting"; label: string }
  | { kind: "unreachable"; home: SavedHome }
  | { kind: "notHome"; address: string }
  | { kind: "compat"; verdict: Exclude<Verdict, "ok">; home: HomeAnswer; at: string }
  | { kind: "tooOld" }
  | { kind: "frame" };

export class Launcher {
  private screen: Screen = { kind: "start", picker: false };
  private readonly app: HTMLElement;
  private readonly stage: HTMLElement;
  private readonly video: HTMLVideoElement;
  private frame: HTMLIFrameElement | null = null;
  private bridge: LauncherBridge | null = null;
  private readyTimer: ReturnType<typeof setTimeout> | null = null;
  private findState: {
    running: boolean;
    found: FoundHome[];
    noIp: boolean;
    abort: { aborted: boolean };
  } = {
    running: false,
    found: [],
    noIp: false,
    abort: { aborted: false },
  };
  private info: HelloInfo | null = null;
  private keys: string[] = [];
  private readonly player: PlayerAdapter;
  private current: Connection | null = null;

  constructor(private readonly d: LauncherDeps) {
    const doc = d.window.document;
    this.app = doc.getElementById("app") ?? doc.body.appendChild(h("main", { id: "app" }));
    this.stage = doc.getElementById("stage") ?? doc.body.appendChild(h("div", { id: "stage" }));
    this.video = doc.createElement("video");
    this.video.className = "ml-plane";
    this.video.hidden = true;
    this.video.setAttribute("playsinline", "");
    this.stage.appendChild(this.video);
    this.player = d.player ? d.player(this.video) : new VideoElementPlayer(this.video);
    onLangChange(() => this.render());
    d.window.addEventListener("keydown", (e) => this.onKey(e), true);
    d.window.addEventListener("message", (e) =>
      this.bridge?.handle({ origin: e.origin, source: e.source, data: e.data })
    );
    doc.addEventListener("visibilitychange", () => this.bridge?.sendVisibility(doc.hidden));
  }

  /** Starts: the too-old page, the last home, or the start screen. */
  async start(): Promise<void> {
    log(
      "launcher",
      `start ${APP_NAME}/${LAUNCHER_VERSION} on ${this.d.platform.name}${BUNDLE_UI ? " (bundled UI)" : ""}`
    );
    const major = chromiumMajor(this.d.window.navigator.userAgent);
    if (major > 0 && major < OLDEST_CHROMIUM) {
      this.show({ kind: "tooOld" });
      return;
    }
    this.keys = this.d.platform.registerKeys();
    const device = await this.d.platform.device();
    this.info = {
      platform: this.d.platform.name,
      model: device.model,
      year: device.year,
      engine: device.engine,
      version: LAUNCHER_VERSION,
      device_caps: this.d.platform.deviceCaps(),
      keys: this.keys,
    };
    log("launcher", `device ${device.model} ${device.year || "?"} ${device.engine}`);
    const params = new URLSearchParams(this.d.window.location.search);
    const last = savedHomes()[0];
    if (last && !params.has("picker")) {
      void this.connectSaved(last);
      return;
    }
    this.show({ kind: "start", picker: false });
  }

  // ---- connecting ---------------------------------------------------------------------

  /** A saved home: its base, else where it was found on the LAN. */
  async connectSaved(home: SavedHome): Promise<void> {
    this.show({ kind: "connecting", label: home.name });
    let answer = await readHome(home.base, this.d.fetch, CONNECT_TIMEOUT_MS);
    let at = home.base;
    if ((!answer || answer.id !== home.id) && home.lan && home.lan !== home.base) {
      answer = await readHome(home.lan, this.d.fetch, CONNECT_TIMEOUT_MS);
      at = home.lan;
    }
    if (!answer || answer.id !== home.id) {
      log("launcher", `saved home ${home.id} did not answer`);
      this.show({ kind: "unreachable", home });
      return;
    }
    await this.connect(at, answer);
  }

  /** An address typed or a home found. */
  async connectAddress(base: string, known?: HomeAnswer): Promise<void> {
    this.show({ kind: "connecting", label: known?.name ?? base });
    const answer = known ?? (await readHome(base, this.d.fetch, CONNECT_TIMEOUT_MS));
    if (!answer) {
      this.show({ kind: "notHome", address: base });
      return;
    }
    await this.connect(base, answer);
  }

  private async connect(foundAt: string, answer: HomeAnswer): Promise<void> {
    const conn = await chooseConnection(foundAt, answer, this.d.fetch);
    const verdict = decide(conn.home, {
      name: APP_NAME,
      platform: this.d.platform.name,
      version: LAUNCHER_VERSION,
      min_api_level: MIN_API_LEVEL,
      built_for_api_level: BUILT_FOR_API_LEVEL,
    });
    log("launcher", `home ${conn.home.id} level ${conn.home.api_level ?? 0}: ${verdict}`);
    if (verdict !== "ok") {
      this.show({ kind: "compat", verdict, home: conn.home, at: foundAt });
      return;
    }
    // a plain-HTTP fallback never replaces a saved HTTPS address (the name may answer again)
    const before = savedHomes().find((x) => x.id === conn.home.id);
    const keep = conn.plain && before && !before.plain ? before : null;
    rememberHome({
      id: conn.home.id,
      name: conn.home.name || conn.home.id,
      base: keep ? keep.base : conn.base,
      plain: keep ? false : conn.plain,
      ...(foundAt.startsWith("http:") ? { lan: foundAt } : keep?.lan ? { lan: keep.lan } : {}),
    });
    this.loadFrame(conn);
  }

  // ---- the iframe and the bridge ------------------------------------------------------

  /** Where /tv/ is loaded from, and the origins its messages may carry. */
  frameTarget(conn: Connection): { src: string; origins: string[]; targetOrigin: string } {
    const q = `bridge=1&lang=${lang()}${conn.plain ? "&plain=1" : ""}`;
    if (BUNDLE_UI) {
      const own = this.d.window.location.origin;
      return {
        src: `ui/index.html?${q}&home=${encodeURIComponent(conn.base)}`,
        // a file:// page reports its origin as "null" or "file://"
        origins: [own, "null", "file://"],
        targetOrigin: "*",
      };
    }
    const origin = originOf(conn.base);
    return { src: `${conn.base}/tv/?${q}`, origins: [origin], targetOrigin: origin };
  }

  private loadFrame(conn: Connection): void {
    this.closeFrame();
    this.current = conn;
    const target = this.frameTarget(conn);
    const frame = this.d.window.document.createElement("iframe");
    frame.className = "ml-frame";
    frame.setAttribute("data-testid", "tv-frame");
    frame.setAttribute("allow", "autoplay; fullscreen");
    frame.style.visibility = "hidden";
    frame.src = target.src;
    this.stage.appendChild(frame);
    this.frame = frame;
    this.bridge = new LauncherBridge({
      frame: () => frame.contentWindow,
      origins: target.origins,
      targetOrigin: target.targetOrigin,
      info: this.info!,
      player: this.player,
      handlers: {
        ready: () => this.frameReady(),
        exit: () => {
          log("launcher", "exit (the UI confirmed)");
          this.d.platform.exit();
        },
        setLanguage: (l: Lang) => setLang(l),
        openHomePicker: () => {
          this.closeFrame();
          this.show({ kind: "start", picker: true });
        },
      },
    });
    log("launcher", `loading ${target.src}`);
    this.readyTimer = setTimeout(() => {
      log("error", `the home's /tv/ did not answer within ${READY_TIMEOUT_MS} ms`);
      const saved = savedHomes().find((x) => x.id === conn.home.id);
      this.closeFrame();
      if (saved) this.show({ kind: "unreachable", home: saved });
      else this.show({ kind: "notHome", address: conn.base });
    }, READY_TIMEOUT_MS);
  }

  private frameReady(): void {
    if (!this.frame) return;
    if (this.readyTimer) clearTimeout(this.readyTimer);
    this.readyTimer = null;
    if (this.screen.kind !== "frame") log("launcher", "the home's /tv/ is ready");
    this.frame.style.visibility = "visible";
    this.screen = { kind: "frame" };
    this.app.hidden = true;
    this.app.textContent = "";
    this.frame.focus();
    this.frame.contentWindow?.focus();
  }

  private closeFrame(): void {
    if (this.readyTimer) clearTimeout(this.readyTimer);
    this.readyTimer = null;
    this.bridge?.dispose();
    this.bridge = null;
    this.player.stop();
    this.frame?.remove();
    this.frame = null;
    this.current = null;
  }

  // ---- keys ---------------------------------------------------------------------------

  private onKey(e: KeyboardEvent): void {
    const action = this.d.platform.keyAction(e);
    if (this.screen.kind === "frame") {
      // a key the launcher's page got while the UI is shown: hand it to the UI
      if (action && this.bridge) {
        e.preventDefault();
        this.bridge.sendKey(action as BridgeAction);
        this.frame?.contentWindow?.focus();
      }
      return;
    }
    if (!action) return;
    if (action === "up" || action === "down" || action === "left" || action === "right") {
      if (moveFocus(this.app, action as Move, dir() === "rtl")) e.preventDefault();
      return;
    }
    if (action === "back") {
      const el = e.target as HTMLElement | null;
      // Backspace-like keys stay in a text field
      if (
        el &&
        el.tagName === "INPUT" &&
        e.key !== "Escape" &&
        e.keyCode !== 10009 &&
        e.keyCode !== 461
      )
        return;
      e.preventDefault();
      this.back();
    }
  }

  private back(): void {
    switch (this.screen.kind) {
      case "find":
        this.findState.abort.aborted = true;
        this.show({ kind: "start", picker: true });
        return;
      case "typed":
      case "notHome":
      case "unreachable":
      case "compat":
        this.show({ kind: "start", picker: true });
        return;
      case "start":
      case "tooOld":
        this.d.platform.exit();
        return;
      default:
        return;
    }
  }

  // ---- screens ------------------------------------------------------------------------

  private show(s: Screen): void {
    this.screen = s;
    if (s.kind !== "frame") this.app.hidden = false;
    this.render();
  }

  private render(): void {
    if (this.screen.kind === "frame") return;
    const doc = this.d.window.document;
    doc.documentElement.dir = dir();
    doc.documentElement.lang = lang();
    this.app.textContent = "";
    this.app.appendChild(this.view(this.screen));
    focusFirst(this.app);
  }

  private view(s: Screen): HTMLElement {
    switch (s.kind) {
      case "start":
        return this.startView();
      case "find":
        return this.findView();
      case "typed":
        return this.typedView(s.error);
      case "connecting":
        return page(
          "connecting",
          t("launcher.title"),
          h(
            "p",
            { class: "ml-lead", attrs: { "aria-live": "polite" } },
            t("launcher.connecting", s.label)
          )
        );
      case "unreachable":
        return page(
          "unreachable",
          t("launcher.unreachable.title", s.home.name),
          h("p", { class: "ml-lead", text: t("launcher.unreachable.body") }),
          h(
            "div",
            { class: "ml-actions" },
            button(t("launcher.tryAgain"), "retry", () => void this.connectSaved(s.home), {
              primary: true,
              autofocus: true,
            }),
            button(t("launcher.chooseAnother"), "picker", () =>
              this.show({ kind: "start", picker: true })
            ),
            button(t("launcher.forget"), "forget", () => {
              forgetHome(s.home.id);
              this.show({ kind: "start", picker: true });
            })
          )
        );
      case "notHome":
        return page(
          "not-home",
          t("launcher.typed.title"),
          h(
            "p",
            { class: "ml-lead" },
            ...splitAround(t("launcher.notHome", "\u0000"), ltr(s.address))
          ),
          h(
            "div",
            { class: "ml-actions" },
            button(t("launcher.tryAgain"), "retry", () => void this.connectAddress(s.address), {
              primary: true,
              autofocus: true,
            }),
            button(t("launcher.back"), "back", () => this.show({ kind: "start", picker: true }))
          )
        );
      case "compat":
        return this.compatView(s.verdict, s.home, s.at);
      case "tooOld":
        return page(
          "too-old",
          t("launcher.title"),
          h("p", { class: "ml-lead", text: t("launcher.tooOld") })
        );
      case "frame":
        return h("div");
    }
  }

  private startView(): HTMLElement {
    const homes = savedHomes();
    const list = homes.length
      ? h(
          "section",
          { class: "ml-section", attrs: { "aria-labelledby": "saved-title" } },
          h("h2", { id: "saved-title", text: t("launcher.saved.title") }),
          ...homes.map((home, i) =>
            button(home.name, `home:${home.id}`, () => void this.connectSaved(home), {
              primary: i === 0,
              autofocus: i === 0,
              testid: `saved-home`,
              sub: home.plain
                ? t("launcher.plainHttp")
                : i === 0
                  ? t("launcher.saved.last")
                  : undefined,
            })
          )
        )
      : null;
    const more = h(
      "details",
      { class: "ml-more", testid: "more-ways" },
      h("summary", { class: "ml-btn", focus: "more", text: t("launcher.more") }),
      button(t("launcher.typed.title"), "typed", () => this.show({ kind: "typed" }))
    );
    return page(
      "start",
      t("launcher.start.title"),
      list,
      h(
        "div",
        { class: "ml-actions" },
        button(t("launcher.find.title"), "find", () => this.startFind(), {
          primary: !homes.length,
          autofocus: !homes.length,
        })
      ),
      more,
      this.footer()
    );
  }

  private footer(): HTMLElement {
    const other: Lang = lang() === "en" ? "ar" : "en";
    return h(
      "footer",
      { class: "ml-footer" },
      h(
        "button",
        {
          class: "ml-btn ml-btn--quiet",
          type: "button",
          focus: "lang",
          testid: "lang",
          lang: other,
          onClick: () => setLang(other),
        },
        other === "ar" ? "العربية" : "English"
      ),
      h(
        "span",
        { class: "ml-version" },
        ...splitAround(t("launcher.version", "\u0000"), ltr(LAUNCHER_VERSION))
      )
    );
  }

  private startFind(): void {
    this.findState.abort.aborted = true;
    const abort = { aborted: false };
    this.findState = { running: true, found: [], noIp: false, abort };
    this.show({ kind: "find" });
    void (async () => {
      const ip = await this.d.platform.localIp();
      if (abort.aborted) return;
      if (!ip) {
        this.findState = { ...this.findState, running: false, noIp: true };
        log("launcher", "find: no address for this TV");
        if (this.screen.kind === "find") this.render();
        return;
      }
      const started = Date.now();
      log("launcher", `find: probing ${ip.replace(/\.\d+$/, ".0")}/24`);
      await probeSubnet(ip, {
        fetch: this.d.fetch,
        signal: abort,
        onFound: (f) => {
          this.findState.found.push(f);
          if (this.screen.kind === "find" && !abort.aborted) this.render();
        },
      });
      if (abort.aborted) return;
      log("launcher", `find: ${this.findState.found.length} home(s) in ${Date.now() - started} ms`);
      this.findState.running = false;
      if (this.screen.kind === "find") this.render();
    })();
  }

  private findView(): HTMLElement {
    const f = this.findState;
    const rows = f.found.map((home, i) =>
      button(
        home.answer.name || home.answer.id,
        `found:${home.answer.id}`,
        () => {
          f.abort.aborted = true;
          void this.connectAddress(home.base, home.answer);
        },
        {
          primary: i === 0,
          autofocus: i === 0,
          testid: "found-home",
          sub: home.base.replace(/^http:\/\//, ""),
        }
      )
    );
    const status = f.noIp
      ? t("launcher.find.noIp")
      : f.running
        ? t("launcher.find.searching")
        : f.found.length
          ? t("launcher.find.found")
          : t("launcher.find.none");
    return page(
      "find",
      t("launcher.find.title"),
      h("p", {
        class: "ml-lead",
        testid: "find-status",
        attrs: { "aria-live": "polite" },
        text: status,
      }),
      rows.length ? h("div", { class: "ml-list" }, ...rows) : null,
      h(
        "div",
        { class: "ml-actions" },
        f.running
          ? null
          : button(t("launcher.find.again"), "find-again", () => this.startFind(), {
              autofocus: !rows.length,
            }),
        button(t("launcher.typed.title"), "typed", () => {
          f.abort.aborted = true;
          this.show({ kind: "typed" });
        }),
        button(t("launcher.back"), "back", () => this.back())
      )
    );
  }

  private typedView(error?: string): HTMLElement {
    const input = h("input", {
      class: "ml-input",
      id: "address",
      focus: "address",
      testid: "address",
      autofocus: true,
      dir: "ltr",
      type: "text",
      attrs: {
        inputmode: "url",
        autocomplete: "off",
        spellcheck: "false",
        "aria-describedby": "address-help",
      },
    }) as HTMLInputElement;
    const submit = () => {
      const base = normalizeBase(input.value);
      if (!base) {
        this.show({ kind: "typed", error: t("launcher.typed.invalid") });
        return;
      }
      void this.connectAddress(base);
    };
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        submit();
      }
    });
    return page(
      "typed",
      t("launcher.typed.title"),
      h("label", {
        class: "ml-label",
        attrs: { for: "address" },
        id: "address-help",
        text: t("launcher.typed.label"),
      }),
      input,
      error ? h("p", { class: "ml-error", role: "alert", text: error }) : null,
      h(
        "div",
        { class: "ml-actions" },
        button(t("launcher.typed.connect"), "connect", submit, { primary: true }),
        button(t("launcher.back"), "back", () => this.back())
      )
    );
  }

  private compatView(verdict: Exclude<Verdict, "ok">, home: HomeAnswer, at: string): HTMLElement {
    const name = home.name || home.id;
    const body =
      verdict === "update_home"
        ? [
            h("p", { class: "ml-lead", text: t("compat.updateHome.admin", name) }),
            h("p", { text: t("compat.updateHome.viewer", name) }),
          ]
        : [
            h("p", { class: "ml-lead", text: t("compat.updateApp.why", name) }),
            h("p", { text: t(`compat.updateApp.${this.d.platform.name}`) }),
          ];
    const version = home.version
      ? h(
          "p",
          { class: "ml-quiet" },
          ...splitAround(t("compat.homeVersion", name, "\u0000"), ltr(home.version))
        )
      : null;
    return page(
      verdict === "update_home" ? "update-home" : "update-app",
      t(verdict === "update_home" ? "compat.updateHome.title" : "compat.updateApp.title"),
      ...body,
      version,
      h(
        "div",
        { class: "ml-actions" },
        button(
          t("launcher.tryAgain"),
          "retry",
          () => {
            const saved = savedHomes().find((x) => x.id === home.id);
            if (saved) void this.connectSaved(saved);
            else void this.connectAddress(at);
          },
          { primary: true, autofocus: true }
        ),
        button(t("launcher.chooseAnother"), "picker", () =>
          this.show({ kind: "start", picker: true })
        )
      ),
      this.footer()
    );
  }

  /** The connection in use (tests). */
  connection(): Connection | null {
    return this.current;
  }
}

/** A screen: its title and body, mirrored by the page's dir. */
function page(id: string, title: string, ...children: (Node | null)[]): HTMLElement {
  return h(
    "section",
    { class: "ml-screen", testid: `screen-${id}`, attrs: { "aria-labelledby": `title-${id}` } },
    h("div", { class: "ml-brand", attrs: { "aria-hidden": "true" } }, t("launcher.title")),
    h("h1", { id: `title-${id}`, text: title }),
    ...children
  );
}

/** A sentence with one run (marked \u0000 in it) replaced by a node, e.g. an address. */
function splitAround(text: string, node: Node): (string | Node)[] {
  const i = text.indexOf("\u0000");
  if (i < 0) return [text];
  return [text.slice(0, i), node, text.slice(i + 1)];
}
