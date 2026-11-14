// One receiver build, /r/<level>/receiver.js (an ES module). The loader at /r/ owns Google's
// CAF context, which must start before any load, and imports the build the first load's
// `api_level` picks; it then calls start() once and hands every load to the build's
// interceptor. This export is the contract between the loader and every build kept on
// cast.maktaba.tv (src/loader/contract.ts): it never changes shape.

import type { BuildHost, BuildReceiver } from "../loader/contract";
import { Receiver } from "./receiver";
import { Screens } from "./ui";

declare const __DEBUG__: boolean;

/** The build's own stylesheet, beside its receiver.js: a build kept for 12 months brings the
 * screens it was made with. */
function stylesheet(): void {
  if (document.getElementById("mk-build-css")) return;
  const link = document.createElement("link");
  link.id = "mk-build-css";
  link.rel = "stylesheet";
  link.href = new URL("receiver.css", import.meta.url).href;
  document.head.appendChild(link);
}

export function start(host: BuildHost): BuildReceiver {
  stylesheet();
  const screens = new Screens(host.root, host.markURL);
  const log = __DEBUG__
    ? (msg: string, extra?: Record<string, unknown>) =>
        console.info(`[maktaba] ${msg}`, extra ?? {})
    : undefined;
  const receiver = new Receiver({
    framework: host.framework,
    context: host.context,
    screens,
    onMessage: host.onMessage,
    log,
  });
  return { onLoad: (req) => receiver.onLoad(req) };
}
