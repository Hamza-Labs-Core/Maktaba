// DesktopGate (Epic 36): inside Maktaba.app's bundled pages, ask the
// shell what the window should show — the first-run setup screens, the
// "starting" screen while this Mac's server comes up, or the app as
// before (client mode). Everywhere else (the browser, the local server's
// own web app inside the shell) it renders its children untouched.
import { useEffect, useState, type ReactNode } from "react";
import { isBundledDesktop, tauriSetupApi, type AppState, type SetupApi } from "../lib/desktopSetup";
import { SetupWizard } from "../pages/Setup/SetupWizard";
import { ServerStarting } from "../pages/Setup/ServerStarting";

interface StatusStep {
  configured: boolean;
  web_url?: string;
  supervisor_url?: string;
}

/** The `status` step: when this Mac is already configured, finish again. */
async function adoptConfigured(api: SetupApi): Promise<string | null> {
  const st = await api.runStep<StatusStep>("status", {});
  if (!st.configured || !st.web_url) return null;
  await api.finishSetup(st.web_url, st.supervisor_url);
  return st.web_url;
}

export function DesktopGate({
  children,
  api = tauriSetupApi,
  bundled = isBundledDesktop(),
}: {
  children: ReactNode;
  api?: SetupApi;
  /** for tests: pretend to run on the app's bundled pages */
  bundled?: boolean;
}) {
  const [state, setState] = useState<AppState | null | "failed">(bundled ? null : "failed");
  useEffect(() => {
    if (!bundled) return;
    api
      .appState()
      .then(async (st) => {
        if (st.target === "setup" && st.server_available && !st.reset) {
          // the app forgot its mode (a reset config dir) but this Mac's
          // server is set up: register it again instead of a second setup
          const adopted = await adoptConfigured(api).catch(() => null);
          if (adopted) return setState({ ...st, target: "starting", url: adopted });
        }
        setState(st);
      })
      .catch(() => setState("failed"));
  }, [api, bundled]);

  if (state === null) return null; // the shell answers (plus the status step on first run)
  if (state === "failed") return <>{children}</>;
  switch (state.target) {
    case "setup":
      return <SetupWizard api={api} />;
    case "starting":
      return <ServerStarting api={api} />;
    case "remote":
      return null; // the shell is loading that Maktaba in the window
    case "bundled":
      return <>{children}</>;
  }
}
