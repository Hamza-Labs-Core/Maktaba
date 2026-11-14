// The registered receiver URL, https://cast.maktaba.tv/r/ (story 58.07). It starts Google's CAF
// context (the start options are the ones every build shares: Shaka for HLS at the pinned
// version, the custom namespace), shows the idle screen, and on the first load imports the
// receiver build the sender's `api_level` picks (builds.ts), or shows "Update the home".

import { constant, type CafFramework, type CafLoadRequest } from "../receiver/caf";
import { langOf } from "../receiver/i18n";
import { Screens } from "../receiver/ui";
import { buildsOf, pickBuild, senderLevel, type Build } from "./builds";
import { NAMESPACE, type BuildModule, type BuildReceiver } from "./contract";

/** The Shaka Player version CAF loads for HLS (useShakaForHls): pinned to a tested release
 * (Google has stopped fixing its older HLS player). */
export const SHAKA_VERSION = "4.15.12";

declare const __DEBUG__: boolean;

interface CastGlobal {
  framework?: CafFramework;
  debug?: {
    CastDebugLogger?: {
      getInstance(): { setEnabled(on: boolean): void; showDebugLogs?(on: boolean): void };
    };
  };
}

async function manifest(): Promise<Build[]> {
  try {
    const res = await fetch(new URL("builds.json", location.href).href, { cache: "no-store" });
    return res.ok ? buildsOf(await res.json()) : [];
  } catch {
    return [];
  }
}

function boot(): void {
  const root = document.getElementById("maktaba");
  if (!root) return;
  const markURL = new URL("mark.svg", location.href).href;
  const screens = new Screens(root, markURL);
  // before any sender speaks: the device's own language
  screens.setLang(langOf(navigator.language));
  const cast = (window as unknown as { cast?: CastGlobal }).cast;
  const fw = cast?.framework;
  if (!fw) return; // opened outside a Cast device: the idle screen alone

  if (__DEBUG__) {
    try {
      const dbg = cast?.debug?.CastDebugLogger?.getInstance();
      dbg?.setEnabled(true);
      dbg?.showDebugLogs?.(true);
    } catch {
      // no debug logger on this SDK
    }
  }

  const ctx = fw.CastReceiverContext.getInstance();
  const pm = ctx.getPlayerManager();
  let handler: ((data: unknown, senderId: string) => void) | null = null;
  ctx.addCustomMessageListener(NAMESPACE, (e) => handler?.(e.data, e.senderId));

  let builds: Promise<Build[]> | null = null;
  let receiver: Promise<BuildReceiver | null> | null = null;

  const failed = (reason: string) => {
    const m = fw.messages;
    const type = constant(m.ErrorType, "LOAD_FAILED", "LOAD_FAILED");
    const err = m.ErrorData ? new m.ErrorData(type) : { type };
    (err as { customData?: unknown }).customData = { error: reason };
    return err;
  };

  pm.setMessageInterceptor(fw.messages.MessageType.LOAD, async (req: CafLoadRequest) => {
    const data = (req.media?.customData ?? req.customData) as Record<string, unknown> | undefined;
    if (!receiver) {
      builds = builds ?? manifest();
      const build = pickBuild(await builds, senderLevel(data?.api_level));
      if (!build) {
        screens.setLang(langOf(data?.lang));
        screens.update();
        try {
          ctx.sendCustomMessage(NAMESPACE, undefined, { type: "error", error: "update" });
        } catch {
          // no sender connected
        }
        return failed("update");
      }
      receiver = import(/* @vite-ignore */ new URL(`${build.path}receiver.js`, location.href).href)
        .then((mod: BuildModule) =>
          mod.start({
            framework: fw,
            context: ctx,
            root,
            markURL,
            onMessage: (fn) => (handler = fn),
          })
        )
        .catch(() => null);
    }
    const r = await receiver;
    if (!r) {
      receiver = null;
      screens.error("receiver.error.home");
      return failed("home");
    }
    return r.onLoad(req);
  });

  ctx.start({
    useShakaForHls: true,
    shakaVersion: SHAKA_VERSION,
    customNamespaces: { [NAMESPACE]: "JSON" },
  });
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
else boot();
